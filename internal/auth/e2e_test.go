package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"english-teacher-app/internal/auth"
	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"
)

func setupTestApp(t *testing.T) (http.Handler, *db.DB, *config.Config) {
	t.Helper()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "e2e_test.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		AdminEmails:   []string{"attila.hitb@gmail.com", "attila.private@gmail.com"},
		SessionSecret: "test-secret-12345678901234567890",
		AppURL:        "http://localhost:8080",
	}

	_ = database.EnsureAdminEmails(cfg.AdminEmails)

	resolver := gemini.NewModelResolver(database, "auto")
	handler := auth.NewHandler(cfg, database, resolver)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/register", handler.HandleRegister)
	mux.HandleFunc("POST /api/auth/login", handler.HandleLogin)
	mux.HandleFunc("POST /api/auth/logout", handler.HandleLogout)
	mux.HandleFunc("GET /api/auth/me", handler.HandleGetMe)
	mux.HandleFunc("POST /api/user/custom-key", handler.HandleSetCustomKey)
	mux.HandleFunc("GET /api/admin/users", handler.RequireAdmin(handler.HandleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users/status", handler.RequireAdmin(handler.HandleAdminUpdateUserStatus))
	mux.HandleFunc("POST /api/admin/users/limit", handler.RequireAdmin(handler.HandleAdminUpdateUserLimit))
	mux.HandleFunc("DELETE /api/admin/users", handler.RequireAdmin(handler.HandleAdminDeleteUser))

	return mux, database, cfg
}

func TestE2E_AuthAndAdminFlow(t *testing.T) {
	app, _, _ := setupTestApp(t)

	// 1. Initial /api/auth/me should return authenticated: false
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	var meResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.NewDecoder(w.Body).Decode(&meResp)
	if meResp.Authenticated {
		t.Fatalf("Expected unauthenticated, got true")
	}

	// 2. Register normal student user
	studentPayload := []byte(`{"name":"Student Bob","email":"bob@example.com","password":"password123"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(studentPayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var studentCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "session_token" {
			studentCookie = c
			break
		}
	}
	if studentCookie == nil {
		t.Fatalf("Expected session_token cookie from registration")
	}

	// 3. Check /api/auth/me as student
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(studentCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	var studentMe struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Email             string `json:"email"`
			Role              string `json:"role"`
			Status            string `json:"status"`
			DailyLimitMinutes int    `json:"daily_limit_minutes"`
		} `json:"user"`
	}
	_ = json.NewDecoder(w.Body).Decode(&studentMe)
	if !studentMe.Authenticated || (studentMe.User.Role != "student" && studentMe.User.Role != "user") {
		t.Fatalf("Unexpected student me: %+v", studentMe)
	}

	// 4. Verify student cannot access /api/admin/users (Forbidden)
	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(studentCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for student on admin endpoint, got %d", w.Code)
	}

	// 5. Register Admin user (attila.hitb@gmail.com)
	adminPayload := []byte(`{"name":"Attila Admin","email":"attila.hitb@gmail.com","password":"secureadminpass"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(adminPayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for admin register, got %d: %s", w.Code, w.Body.String())
	}
	var adminCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "session_token" {
			adminCookie = c
			break
		}
	}
	if adminCookie == nil {
		t.Fatalf("Expected session_token cookie for admin")
	}

	// 6. Check /api/auth/me as Admin
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(adminCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	var adminMe struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Email  string `json:"email"`
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"user"`
	}
	_ = json.NewDecoder(w.Body).Decode(&adminMe)
	if !adminMe.Authenticated || adminMe.User.Role != "admin" || adminMe.User.Status != "active" {
		t.Fatalf("Expected active admin role, got %+v", adminMe)
	}

	// 7. Admin successfully accesses /api/admin/users
	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(adminCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var usersList []map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&usersList)
	if len(usersList) < 2 {
		t.Fatalf("Expected at least 2 users in admin list, got %d", len(usersList))
	}

	// Find student user id
	var studentID string
	for _, u := range usersList {
		if u["email"] == "bob@example.com" {
			studentID = u["id"].(string)
			break
		}
	}
	if studentID == "" {
		t.Fatalf("Could not find student in admin list")
	}

	// 8. Admin updates student limit to 60 minutes
	limitBody := []byte(`{"user_id":"` + studentID + `","limit_minutes":60}`)
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users/limit", bytes.NewReader(limitBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for limit update, got %d: %s", w.Code, w.Body.String())
	}

	// 9. Admin updates student status to active
	statusBody := []byte(`{"user_id":"` + studentID + `","status":"active"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users/status", bytes.NewReader(statusBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for status update, got %d: %s", w.Code, w.Body.String())
	}

	// 10. Student checks /api/auth/me and sees 60 minutes limit and active status
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(studentCookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	_ = json.NewDecoder(w.Body).Decode(&studentMe)
	if studentMe.User.DailyLimitMinutes != 60 || studentMe.User.Status != "active" {
		t.Fatalf("Expected 60 min and active status, got: %+v", studentMe)
	}
}
