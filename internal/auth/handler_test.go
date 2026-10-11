package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"
)

func setupTestEnv(t *testing.T) (*Handler, *db.DB) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "auth_test.db")

	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	cfg := &config.Config{
		Port:        "8080",
		DBPath:      dbPath,
		AdminEmails: []string{"attila.hitb@gmail.com", "attila.private@gmail.com"},
		AppURL:      "http://localhost:8080",
	}

	resolver := gemini.NewModelResolver(database, "auto")
	handler := NewHandler(cfg, database, resolver)
	return handler, database
}

func TestRegisterAndLogin(t *testing.T) {
	handler, database := setupTestEnv(t)
	defer database.Close()

	// 1. Register a student user
	regPayload := map[string]string{
		"email":        "teststudent@example.com",
		"password":     "secret123",
		"display_name": "Test Student",
	}
	body, _ := json.Marshal(regPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	cookie := w.Result().Cookies()
	var sessionToken string
	for _, c := range cookie {
		if c.Name == "session_token" {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatalf("Expected session_token cookie to be set")
	}

	// 2. Call /api/auth/me with session cookie
	reqMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqMe.AddCookie(&http.Cookie{Name: "session_token", Value: sessionToken})
	wMe := httptest.NewRecorder()

	handler.HandleGetMe(wMe, reqMe)
	if wMe.Code != http.StatusOK {
		t.Fatalf("HandleGetMe failed: %d", wMe.Code)
	}

	var meResp struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			Status      string `json:"status"`
		} `json:"user"`
	}
	_ = json.NewDecoder(wMe.Body).Decode(&meResp)
	if !meResp.Authenticated || meResp.User.Email != "teststudent@example.com" {
		t.Fatalf("Expected authenticated teststudent, got %+v", meResp)
	}
	if meResp.User.Role != "student" || meResp.User.Status != "pending" {
		t.Fatalf("Expected student/pending status for new user, got role=%s status=%s", meResp.User.Role, meResp.User.Status)
	}

	// 3. Register with admin email -> should be admin and active immediately!
	adminPayload := map[string]string{
		"email":        "attila.hitb@gmail.com",
		"password":     "adminpassword123",
		"display_name": "Attila Admin",
	}
	adminBody, _ := json.Marshal(adminPayload)
	reqAdmin := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(adminBody))
	wAdmin := httptest.NewRecorder()

	handler.HandleRegister(wAdmin, reqAdmin)
	if wAdmin.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for admin register, got %d: %s", wAdmin.Code, wAdmin.Body.String())
	}

	var adminCookie string
	for _, c := range wAdmin.Result().Cookies() {
		if c.Name == "session_token" {
			adminCookie = c.Value
		}
	}

	reqAdminMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqAdminMe.AddCookie(&http.Cookie{Name: "session_token", Value: adminCookie})
	wAdminMe := httptest.NewRecorder()

	handler.HandleGetMe(wAdminMe, reqAdminMe)
	var adminMeResp struct {
		User struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"user"`
	}
	_ = json.NewDecoder(wAdminMe.Body).Decode(&adminMeResp)
	if adminMeResp.User.Role != "admin" || adminMeResp.User.Status != "active" {
		t.Fatalf("Expected admin/active, got role=%s status=%s", adminMeResp.User.Role, adminMeResp.User.Status)
	}
}

func TestBootstrappedAdminCanLoginDirectly(t *testing.T) {
	handler, database := setupTestEnv(t)
	defer database.Close()

	// Bootstrap admin accounts (creates user with empty password)
	err := database.EnsureAdminEmails([]string{"attila.hitb@gmail.com"})
	if err != nil {
		t.Fatalf("EnsureAdminEmails failed: %v", err)
	}

	// Now admin directly logs in via HandleLogin with a password
	loginPayload := map[string]string{
		"email":    "attila.hitb@gmail.com",
		"password": "mySecurePassword123",
	}
	body, _ := json.Marshal(loginPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for bootstrapped admin login, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the session works and user is admin
	cookies := w.Result().Cookies()
	var token string
	for _, c := range cookies {
		if c.Name == "session_token" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatalf("Expected session_token cookie")
	}

	reqMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqMe.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	wMe := httptest.NewRecorder()
	handler.HandleGetMe(wMe, reqMe)

	var meResp struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"user"`
	}
	_ = json.NewDecoder(wMe.Body).Decode(&meResp)
	if !meResp.Authenticated || meResp.User.Role != "admin" || meResp.User.Status != "active" {
		t.Fatalf("Expected authenticated active admin, got %+v", meResp)
	}
}

func TestSessionCookieSecureOnHTTPS(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "https_auth_test.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	cfg := &config.Config{
		Port:        "8080",
		DBPath:      dbPath,
		AdminEmails: []string{"attila.hitb@gmail.com"},
		AppURL:      "https://english-teacher-app.example.com",
	}

	resolver := gemini.NewModelResolver(database, "auto")
	handler := NewHandler(cfg, database, resolver)

	// 1. Check setSessionCookie produces Secure=true and SameSite=Lax
	wSet := httptest.NewRecorder()
	handler.setSessionCookie(wSet, "dummy-token")
	var setCookie *http.Cookie
	for _, c := range wSet.Result().Cookies() {
		if c.Name == "session_token" {
			setCookie = c
			break
		}
	}
	if setCookie == nil {
		t.Fatalf("Expected session_token cookie")
	}
	if !setCookie.Secure {
		t.Errorf("Expected Secure=true on HTTPS for setSessionCookie")
	}
	if setCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Expected SameSite=Lax on HTTPS for setSessionCookie")
	}

	// 2. Check clearSessionCookie produces Secure=true and SameSite=Lax so browser allows deleting Secure cookie
	wClear := httptest.NewRecorder()
	handler.clearSessionCookie(wClear)
	var clearCookie *http.Cookie
	for _, c := range wClear.Result().Cookies() {
		if c.Name == "session_token" {
			clearCookie = c
			break
		}
	}
	if clearCookie == nil {
		t.Fatalf("Expected session_token cookie on clear")
	}
	if !clearCookie.Secure {
		t.Errorf("Expected Secure=true on HTTPS for clearSessionCookie to allow browser deletion")
	}
	if clearCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Expected SameSite=Lax on HTTPS for clearSessionCookie")
	}
	if clearCookie.MaxAge >= 0 {
		t.Errorf("Expected MaxAge < 0 for clearing cookie, got %d", clearCookie.MaxAge)
	}
}

func TestStatelessSignedSessionTokens(t *testing.T) {
	// Instance 1: Creates the session token
	tempDir1 := t.TempDir()
	dbPath1 := filepath.Join(tempDir1, "instance1.db")
	db1, err := db.InitDB(dbPath1)
	if err != nil {
		t.Fatalf("InitDB 1 failed: %v", err)
	}
	defer db1.Close()

	cfg1 := &config.Config{
		Port:          "8080",
		DBPath:        dbPath1,
		AdminEmails:   []string{"attila.hitb@gmail.com"},
		SessionSecret: "shared-test-secret-123",
	}
	resolver1 := gemini.NewModelResolver(db1, "auto")
	handler1 := NewHandler(cfg1, db1, resolver1)

	// User logs in on Instance 1
	claims := SessionClaims{
		UserID:    "user-uuid-123",
		Email:     "attila.hitb@gmail.com",
		Role:      "admin",
		Status:    "active",
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}
	signedToken := handler1.signSessionToken(claims)
	if signedToken == "" {
		t.Fatalf("Expected non-empty signedToken")
	}

	// Instance 2: Separate DB, represents a different Cloud Run instance or a container restart
	tempDir2 := t.TempDir()
	dbPath2 := filepath.Join(tempDir2, "instance2.db")
	db2, err := db.InitDB(dbPath2)
	if err != nil {
		t.Fatalf("InitDB 2 failed: %v", err)
	}
	defer db2.Close()

	cfg2 := &config.Config{
		Port:          "8080",
		DBPath:        dbPath2,
		AdminEmails:   []string{"attila.hitb@gmail.com"},
		SessionSecret: "shared-test-secret-123", // Same secret shared via environment
	}
	resolver2 := gemini.NewModelResolver(db2, "auto")
	handler2 := NewHandler(cfg2, db2, resolver2)

	// Instance 2 receives the request with signedToken from Instance 1
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedToken})

	user2 := handler2.GetUserFromRequest(req)
	if user2 == nil {
		t.Fatalf("Expected Instance 2 to validate token and return user")
	}
	if user2.Email != "attila.hitb@gmail.com" {
		t.Errorf("Expected email attila.hitb@gmail.com, got %s", user2.Email)
	}
	if user2.Role != "admin" {
		t.Errorf("Expected role admin, got %s", user2.Role)
	}
	if user2.Status != "active" {
		t.Errorf("Expected status active, got %s", user2.Status)
	}

	// Verify RequireAdmin on Instance 2 succeeds
	adminHandlerCalled := false
	dummyAdminHandler := handler2.RequireAdmin(func(w http.ResponseWriter, r *http.Request) {
		adminHandlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	dummyAdminHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on Instance 2 RequireAdmin, got %d: %s", w.Code, w.Body.String())
	}
	if !adminHandlerCalled {
		t.Errorf("Expected adminHandler to be called")
	}
}

