package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUserAndSessionManagement(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_users.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	// 1. Create a student user
	student, err := database.CreateUser("student@example.com", "hashed_pw", "local", "", "student", "active", "Student One")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if student.Email != "student@example.com" {
		t.Fatalf("Expected email student@example.com, got %s", student.Email)
	}

	// 2. Fetch by email and by ID
	fetched, err := database.GetUserByEmail("student@example.com")
	if err != nil || fetched == nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if fetched.ID != student.ID {
		t.Fatalf("ID mismatch: %s vs %s", fetched.ID, student.ID)
	}

	// 3. Create Session
	session, err := database.CreateSession(student.ID, 24*time.Hour)
	if err != nil || session == nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	sFetched, err := database.GetSession(session.Token)
	if err != nil || sFetched == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if sFetched.UserID != student.ID {
		t.Fatalf("Session user mismatch")
	}

	// 4. EnsureAdminEmails bootstrap
	adminEmails := []string{"attila.hitb@gmail.com", "attila.private@gmail.com"}
	if err := database.EnsureAdminEmails(adminEmails); err != nil {
		t.Fatalf("EnsureAdminEmails failed: %v", err)
	}

	admin1, err := database.GetUserByEmail("attila.hitb@gmail.com")
	if err != nil || admin1 == nil {
		t.Fatalf("Admin1 not found")
	}
	if admin1.Role != "admin" || admin1.Status != "active" {
		t.Fatalf("Admin1 role/status mismatch: role=%s status=%s", admin1.Role, admin1.Status)
	}

	// 5. Quota check on Student
	allowed, usedMin, limitMin, isCustom, _, err := database.CheckUserQuota(student.ID)
	if err != nil {
		t.Fatalf("CheckUserQuota failed: %v", err)
	}
	if !allowed || usedMin != 0 || limitMin != 45 || isCustom {
		t.Fatalf("Unexpected quota result: allowed=%v used=%d limit=%d custom=%v", allowed, usedMin, limitMin, isCustom)
	}

	// Log a 50-minute call for student
	err = database.LogCall(&CallLog{
		UserID:          student.ID,
		PersonaID:       "teacher-sarah",
		StartedAt:       time.Now().Add(-50 * time.Minute),
		EndedAt:         time.Now(),
		DurationSeconds: 50 * 60,
	})
	if err != nil {
		t.Fatalf("LogCall failed: %v", err)
	}

	// Re-check quota - should now exceed 45 minutes!
	allowed, usedMin, limitMin, _, _, err = database.CheckUserQuota(student.ID)
	if err != nil {
		t.Fatalf("CheckUserQuota after call failed: %v", err)
	}
	if allowed {
		t.Fatalf("Expected student to be blocked after 50 minutes call on 45 min limit!")
	}
	if usedMin < 50 {
		t.Fatalf("Expected usedMin >= 50, got %d", usedMin)
	}

	// Now student adds Custom API Key (BYOK)
	err = database.UpdateUserCustomKey(student.ID, "AIzaSyCustomKeyTest123")
	if err != nil {
		t.Fatalf("UpdateUserCustomKey failed: %v", err)
	}

	// Re-check quota - should now be ALLOWED because BYOK!
	allowed, _, _, isCustom, customKey, err := database.CheckUserQuota(student.ID)
	if err != nil {
		t.Fatalf("CheckUserQuota with BYOK failed: %v", err)
	}
	if !allowed || !isCustom || customKey != "AIzaSyCustomKeyTest123" {
		t.Fatalf("Expected BYOK to allow call! allowed=%v isCustom=%v key=%s", allowed, isCustom, customKey)
	}

	// Check Admin stats view
	stats, err := database.GetAllUsersWithStats()
	if err != nil {
		t.Fatalf("GetAllUsersWithStats failed: %v", err)
	}
	if len(stats) < 3 { // student + 2 admins
		t.Fatalf("Expected at least 3 users in stats, got %d", len(stats))
	}
}
