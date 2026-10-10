package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
