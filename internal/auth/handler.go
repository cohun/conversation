package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

func maskAPIKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "***"
	}
	return k[:4] + "..." + k[len(k)-4:]
}

var googleEndpoint = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

type contextKey string

const UserContextKey contextKey = "auth_user"

type Handler struct {
	cfg         *config.Config
	database    *db.DB
	resolver    *gemini.ModelResolver
	oauthConfig *oauth2.Config
}

func NewHandler(cfg *config.Config, database *db.DB, resolver *gemini.ModelResolver) *Handler {
	var oauthCfg *oauth2.Config
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		oauthCfg = &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.AppURL + "/api/auth/google/callback",
			Scopes: []string{
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
				"openid",
			},
			Endpoint: googleEndpoint,
		}
	}

	return &Handler{
		cfg:         cfg,
		database:    database,
		resolver:    resolver,
		oauthConfig: oauthCfg,
	}
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, token string) {
	secure := strings.HasPrefix(h.cfg.AppURL, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(30 * 24 * time.Hour),
		MaxAge:   30 * 24 * 3600,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter) {
	secure := strings.HasPrefix(h.cfg.AppURL, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func jsonErr(w http.ResponseWriter, status int, msg string) {
	jsonResp(w, status, map[string]string{"error": msg})
}

// GetUserFromRequest reads session_token from cookie or Authorization header and returns user
func (h *Handler) GetUserFromRequest(r *http.Request) *db.User {
	var token string
	if cookie, err := r.Cookie("session_token"); err == nil && cookie.Value != "" {
		token = cookie.Value
	}
	if token == "" {
		authHdr := r.Header.Get("Authorization")
		if strings.HasPrefix(authHdr, "Bearer ") {
			token = strings.TrimPrefix(authHdr, "Bearer ")
		}
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return nil
	}

	session, err := h.database.GetSession(token)
	if err != nil || session == nil {
		return nil
	}

	user, err := h.database.GetUserByID(session.UserID)
	if err != nil || user == nil {
		return nil
	}

	// Always ensure configured admin emails retain active admin role
	if h.cfg.IsAdminEmail(user.Email) && (user.Role != "admin" || user.Status != "active") {
		_ = h.database.UpdateUserRole(user.ID, "admin")
		_ = h.database.UpdateUserStatus(user.ID, "active")
		user.Role = "admin"
		user.Status = "active"
	}

	return user
}

// Middleware: Authenticate attaches user to context
func (h *Handler) Authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := h.GetUserFromRequest(r)
		if user == nil {
			jsonErr(w, http.StatusUnauthorized, "Authentication required. Please sign in.")
			return
		}
		if user.Status == "blocked" {
			jsonErr(w, http.StatusForbidden, "Your account has been suspended by an administrator.")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// Middleware: RequireAdmin ensures caller is admin
func (h *Handler) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := h.GetUserFromRequest(r)
		if user == nil || user.Role != "admin" {
			jsonErr(w, http.StatusForbidden, "Administrator privileges required.")
			return
		}
		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// POST /api/auth/register
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		jsonErr(w, http.StatusBadRequest, "Please provide a valid email address.")
		return
	}
	if len(body.Password) < 6 {
		jsonErr(w, http.StatusBadRequest, "Password must be at least 6 characters.")
		return
	}

	existing, err := h.database.GetUserByEmail(email)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	var user *db.User
	if existing != nil {
		if existing.PasswordHash != "" {
			jsonErr(w, http.StatusConflict, "An account with this email already exists.")
			return
		}
		// Account was pre-seeded (e.g. bootstrap admin) with no password yet: claim it
		if err := h.database.UpdateUserPassword(existing.ID, string(hashed)); err != nil {
			jsonErr(w, http.StatusInternalServerError, "Failed to set password: "+err.Error())
			return
		}
		if h.cfg.IsAdminEmail(existing.Email) {
			_ = h.database.UpdateUserRole(existing.ID, "admin")
			_ = h.database.UpdateUserStatus(existing.ID, "active")
			existing.Role = "admin"
			existing.Status = "active"
		}
		user = existing
	} else {
		role := "student"
		status := "pending"
		if h.cfg.IsAdminEmail(email) {
			role = "admin"
			status = "active"
		}

		displayName := strings.TrimSpace(body.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(body.Name)
		}
		user, err = h.database.CreateUser(email, string(hashed), "local", "", role, status, displayName)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "Failed to create user: "+err.Error())
			return
		}
	}

	// Create session
	session, err := h.database.CreateSession(user.ID, 30*24*time.Hour)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to create session")
		return
	}
	h.setSessionCookie(w, session.Token)

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"user":    user,
		"message": "Account created successfully.",
	})
}

// POST /api/auth/login
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	email := strings.ToLower(strings.TrimSpace(body.Email))
	user, err := h.database.GetUserByEmail(email)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}
	if user == nil {
		jsonErr(w, http.StatusUnauthorized, "Invalid email or password.")
		return
	}

	if user.PasswordHash == "" {
		// The account was bootstrapped (e.g. admin) or pre-created without a password.
		// Allow the user to claim and set their password now.
		if len(body.Password) < 6 {
			jsonErr(w, http.StatusBadRequest, "Please provide a password of at least 6 characters.")
			return
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}
		if err := h.database.UpdateUserPassword(user.ID, string(hashed)); err != nil {
			jsonErr(w, http.StatusInternalServerError, "Failed to save password: "+err.Error())
			return
		}
		user.PasswordHash = string(hashed)
	} else {
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)); err != nil {
			jsonErr(w, http.StatusUnauthorized, "Invalid email or password.")
			return
		}
	}

	// Update admin status if email matches configured admins
	if h.cfg.IsAdminEmail(user.Email) && (user.Role != "admin" || user.Status != "active") {
		_ = h.database.UpdateUserRole(user.ID, "admin")
		_ = h.database.UpdateUserStatus(user.ID, "active")
		user.Role = "admin"
		user.Status = "active"
	}

	session, err := h.database.CreateSession(user.ID, 30*24*time.Hour)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to create session")
		return
	}
	h.setSessionCookie(w, session.Token)

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"user":    user,
		"message": "Logged in successfully.",
	})
}

// POST /api/auth/logout
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session_token"); err == nil && cookie.Value != "" {
		_ = h.database.DeleteSession(cookie.Value)
	}
	h.clearSessionCookie(w)
	jsonResp(w, http.StatusOK, map[string]interface{}{"success": true})
}

// GET /api/auth/me
func (h *Handler) HandleGetMe(w http.ResponseWriter, r *http.Request) {
	user := h.GetUserFromRequest(r)
	if user == nil {
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"authenticated": false,
		})
		return
	}

	profile, _ := h.database.GetUserProfile(user.ID)
	_, usedMin, limitMin, isCustom, _, _ := h.database.CheckUserQuota(user.ID)

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"user": map[string]interface{}{
			"id":                  user.ID,
			"email":               user.Email,
			"display_name":        profile.DisplayName,
			"phonetic_note":       profile.PhoneticNote,
			"role":                user.Role,
			"status":              user.Status,
			"daily_limit_minutes": user.DailyLimitMinutes,
			"today_used_minutes":  usedMin,
			"today_limit_minutes": limitMin,
			"has_custom_key":      user.CustomAPIKey != "",
			"masked_custom_key":   maskAPIKey(user.CustomAPIKey),
			"is_custom_key":       isCustom,
		},
	})
}

// GET /api/auth/google/login
func (h *Handler) HandleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if h.oauthConfig == nil {
		http.Redirect(w, r, "/?auth_error=google_oauth_not_configured", http.StatusTemporaryRedirect)
		return
	}

	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	secure := strings.HasPrefix(h.cfg.AppURL, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	url := h.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// GET /api/auth/google/callback
func (h *Handler) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if h.oauthConfig == nil {
		http.Redirect(w, r, "/?auth_error=google_not_configured", http.StatusTemporaryRedirect)
		return
	}

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Redirect(w, r, "/?auth_error=invalid_state", http.StatusTemporaryRedirect)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/?auth_error=missing_code", http.StatusTemporaryRedirect)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	token, err := h.oauthConfig.Exchange(ctx, code)
	if err != nil {
		log.Printf("[Auth] Google OAuth exchange failed: %v", err)
		http.Redirect(w, r, "/?auth_error=exchange_failed", http.StatusTemporaryRedirect)
		return
	}

	client := h.oauthConfig.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		log.Printf("[Auth] Failed to fetch Google userinfo: %v", err)
		http.Redirect(w, r, "/?auth_error=userinfo_failed", http.StatusTemporaryRedirect)
		return
	}
	defer resp.Body.Close()

	var googleUser struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&googleUser); err != nil {
		http.Redirect(w, r, "/?auth_error=decode_failed", http.StatusTemporaryRedirect)
		return
	}

	email := strings.ToLower(strings.TrimSpace(googleUser.Email))
	if email == "" {
		http.Redirect(w, r, "/?auth_error=no_email", http.StatusTemporaryRedirect)
		return
	}

	user, err := h.database.GetUserByEmail(email)
	if err != nil {
		http.Redirect(w, r, "/?auth_error=db_error", http.StatusTemporaryRedirect)
		return
	}

	role := "student"
	status := "pending"
	if h.cfg.IsAdminEmail(email) {
		role = "admin"
		status = "active"
	}

	googleName := strings.TrimSpace(googleUser.Name)
	if googleName == "" {
		parts := strings.Split(email, "@")
		clean := strings.ReplaceAll(parts[0], ".", " ")
		clean = strings.ReplaceAll(clean, "_", " ")
		googleName = strings.Title(strings.TrimSpace(clean))
	}

	if user == nil {
		// New user from Google
		user, err = h.database.CreateUser(email, "", "google", googleUser.ID, role, status, googleName)
		if err != nil {
			http.Redirect(w, r, "/?auth_error=create_user_failed", http.StatusTemporaryRedirect)
			return
		}
	} else {
		// Existing user
		if user.GoogleSub == "" {
			_ = h.database.UpdateUserGoogleSub(user.ID, googleUser.ID)
		}
		if h.cfg.IsAdminEmail(email) {
			_ = h.database.UpdateUserRole(user.ID, "admin")
			_ = h.database.UpdateUserStatus(user.ID, "active")
		}
	}

	// Create session
	session, err := h.database.CreateSession(user.ID, 30*24*time.Hour)
	if err != nil {
		http.Redirect(w, r, "/?auth_error=session_failed", http.StatusTemporaryRedirect)
		return
	}
	h.setSessionCookie(w, session.Token)

	// Clean up state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.cfg.AppURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/?auth_success=1", http.StatusTemporaryRedirect)
}

// POST /api/user/custom-key (BYOK: Add/remove custom Gemini API Key)
func (h *Handler) HandleSetCustomKey(w http.ResponseWriter, r *http.Request) {
	user := h.GetUserFromRequest(r)
	if user == nil {
		jsonErr(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	key := strings.TrimSpace(body.APIKey)
	if key != "" {
		// Quick validation of user's key against Google Gemini API
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()

		models, err := h.resolver.DiscoverModels(ctx, key)
		if err != nil || len(models) == 0 {
			jsonErr(w, http.StatusBadRequest, "Invalid Gemini API Key. Google AI Studio rejected the key.")
			return
		}
	}

	if err := h.database.UpdateUserCustomKey(user.ID, key); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to update custom key: "+err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"has_custom_key": key != "",
		"masked_key":     maskAPIKey(key),
		"message":        "Custom Gemini API Key saved successfully.",
	})
}

// Admin Endpoints
// GET /api/admin/users
func (h *Handler) HandleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.database.GetAllUsersWithStats()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to load user stats: "+err.Error())
		return
	}
	jsonResp(w, http.StatusOK, users)
}

// POST /api/admin/users/status
func (h *Handler) HandleAdminUpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Status string `json:"status"` // "active", "pending", "blocked"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	status := strings.ToLower(strings.TrimSpace(body.Status))
	if status != "active" && status != "pending" && status != "blocked" {
		jsonErr(w, http.StatusBadRequest, "Status must be 'active', 'pending', or 'blocked'.")
		return
	}

	targetUser, err := h.database.GetUserByID(body.UserID)
	if err != nil || targetUser == nil {
		jsonErr(w, http.StatusNotFound, "User not found")
		return
	}

	// Prevent blocking primary admin
	if h.cfg.IsAdminEmail(targetUser.Email) && status != "active" {
		jsonErr(w, http.StatusBadRequest, "Cannot block primary administrator account.")
		return
	}

	if err := h.database.UpdateUserStatus(body.UserID, status); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to update status: "+err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{"success": true, "status": status})
}

// POST /api/admin/users/limit
func (h *Handler) HandleAdminUpdateUserLimit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID       string `json:"user_id"`
		LimitMinutes int    `json:"limit_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.LimitMinutes < 5 || body.LimitMinutes > 1440 {
		jsonErr(w, http.StatusBadRequest, "Daily limit must be between 5 and 1440 minutes.")
		return
	}

	if err := h.database.UpdateUserDailyLimit(body.UserID, body.LimitMinutes); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to update daily limit: "+err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{"success": true, "limit_minutes": body.LimitMinutes})
}

// DELETE /api/admin/users
func (h *Handler) HandleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		jsonErr(w, http.StatusBadRequest, "Missing user_id")
		return
	}

	targetUser, err := h.database.GetUserByID(userID)
	if err != nil || targetUser == nil {
		jsonErr(w, http.StatusNotFound, "User not found")
		return
	}

	if h.cfg.IsAdminEmail(targetUser.Email) {
		jsonErr(w, http.StatusBadRequest, "Cannot delete primary administrator account.")
		return
	}

	if err := h.database.DeleteUser(userID); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Failed to delete user: "+err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{"success": true})
}
