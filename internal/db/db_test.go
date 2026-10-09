package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDBInitializationAndSeeds(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	// Verify User Profile seed
	profile, err := database.GetUserProfile("default_user")
	if err != nil {
		t.Fatalf("GetUserProfile failed: %v", err)
	}
	if profile.DisplayName != "Alex" {
		t.Errorf("Expected display name 'Alex', got '%s'", profile.DisplayName)
	}

	// Verify Personas seed
	personas, err := database.ListPersonas("default_user")
	if err != nil {
		t.Fatalf("ListPersonas failed: %v", err)
	}
	if len(personas) < 3 {
		t.Fatalf("Expected at least 3 personas, got %d", len(personas))
	}

	// Check Sarah
	foundSarah := false
	for _, p := range personas {
		if p.ID == "teacher-sarah" {
			foundSarah = true
			if p.Mode != "teacher" {
				t.Errorf("Expected Sarah to be teacher mode, got '%s'", p.Mode)
			}
			if p.VoiceName != "Aoede" {
				t.Errorf("Expected Sarah voice 'Aoede', got '%s'", p.VoiceName)
			}
			if !p.IsDefault {
				t.Errorf("Expected Sarah to be default persona")
			}
		}
	}
	if !foundSarah {
		t.Errorf("teacher-sarah persona was not found in seeds")
	}

	// Verify Settings seed
	modelSetting, err := database.GetSetting("default_live_model")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if modelSetting != "auto" {
		t.Errorf("Expected default_live_model to be 'auto', got '%s'", modelSetting)
	}
}

func TestUserProfileUpdate(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	err = database.UpdateUserProfile("default_user", "Sándor", "Sándor (SHAHN-dor)")
	if err != nil {
		t.Fatalf("UpdateUserProfile failed: %v", err)
	}

	p, err := database.GetUserProfile("default_user")
	if err != nil {
		t.Fatalf("GetUserProfile failed: %v", err)
	}
	if p.DisplayName != "Sándor" || p.PhoneticNote != "Sándor (SHAHN-dor)" {
		t.Errorf("Profile update mismatch: %+v", p)
	}
}

func TestSavedConversations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	conv := &SavedConversation{
		ID:           "test-conv-1",
		UserID:       "default_user",
		PersonaID:    "teacher-sarah",
		TopicTitle:   "Past Perfect vs Simple Past",
		Summary:      "Practiced narrating vacation stories using past perfect tense.",
		KeyPoints:    "had gone, had seen, chronological sequence",
		OpenQuestion: "What had you packed before realizing your passport was expired?",
	}

	if err := database.SaveConversation(conv); err != nil {
		t.Fatalf("SaveConversation failed: %v", err)
	}

	list, err := database.ListConversations("default_user")
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Expected 1 saved conversation, got %d", len(list))
	}
	if list[0].TopicTitle != "Past Perfect vs Simple Past" {
		t.Errorf("Unexpected title: %s", list[0].TopicTitle)
	}

	// Update Resumed
	if err := database.UpdateConversationResumed("test-conv-1"); err != nil {
		t.Fatalf("UpdateConversationResumed failed: %v", err)
	}

	// Delete
	if err := database.DeleteConversation("test-conv-1"); err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}
	listAfter, _ := database.ListConversations("default_user")
	if len(listAfter) != 0 {
		t.Errorf("Expected 0 saved conversations after delete, got %d", len(listAfter))
	}

	_ = os.Remove(dbPath)
}
