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

func TestUserPersonaOverridesAndReset(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_override.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	user1 := "user-alice"
	user2 := "user-bob"

	// 1. User 1 customizes Sarah
	err = database.SaveUserPersonaOverride(user1, "teacher-sarah", "Sarah (Alice's Edition)", "Specialized practice", "Custom prompt for Alice", "Kore", "free_chat")
	if err != nil {
		t.Fatalf("SaveUserPersonaOverride failed: %v", err)
	}

	// 2. Verify User 1 sees their custom version of Sarah
	p1, err := database.GetPersonaForUser("teacher-sarah", user1)
	if err != nil {
		t.Fatalf("GetPersonaForUser for user1 failed: %v", err)
	}
	if p1.Name != "Sarah (Alice's Edition)" || p1.VoiceName != "Kore" || p1.Mode != "free_chat" || !p1.HasOverride {
		t.Errorf("User 1 did not get overridden Sarah: %+v", p1)
	}

	// 3. Verify User 1's ListPersonas includes the override
	list1, err := database.ListPersonas(user1)
	if err != nil {
		t.Fatalf("ListPersonas for user1 failed: %v", err)
	}
	var sarah1 *Persona
	for _, p := range list1 {
		if p.ID == "teacher-sarah" {
			sarah1 = &p
			break
		}
	}
	if sarah1 == nil || sarah1.Name != "Sarah (Alice's Edition)" || !sarah1.HasOverride {
		t.Errorf("ListPersonas for user1 failed to reflect override: %+v", sarah1)
	}

	// 4. Verify User 2 STILL SEES pristine default Sarah (unchanged!)
	p2, err := database.GetPersonaForUser("teacher-sarah", user2)
	if err != nil {
		t.Fatalf("GetPersonaForUser for user2 failed: %v", err)
	}
	if p2.Name != "Sarah" || p2.VoiceName != "Aoede" || p2.Mode != "teacher" || p2.HasOverride {
		t.Errorf("User 2 was affected by User 1's override! Got: %+v", p2)
	}

	list2, err := database.ListPersonas(user2)
	if err != nil {
		t.Fatalf("ListPersonas for user2 failed: %v", err)
	}
	var sarah2 *Persona
	for _, p := range list2 {
		if p.ID == "teacher-sarah" {
			sarah2 = &p
			break
		}
	}
	if sarah2 == nil || sarah2.Name != "Sarah" || sarah2.HasOverride {
		t.Errorf("ListPersonas for user2 was affected by User 1's override: %+v", sarah2)
	}

	// 5. User 1 resets Sarah to defaults
	err = database.ResetUserPersonaOverride(user1, "teacher-sarah")
	if err != nil {
		t.Fatalf("ResetUserPersonaOverride failed: %v", err)
	}

	// 6. User 1 now sees pristine default Sarah again
	p1AfterReset, err := database.GetPersonaForUser("teacher-sarah", user1)
	if err != nil {
		t.Fatalf("GetPersonaForUser after reset failed: %v", err)
	}
	if p1AfterReset.Name != "Sarah" || p1AfterReset.VoiceName != "Aoede" || p1AfterReset.HasOverride {
		t.Errorf("User 1 Sarah was not restored to default: %+v", p1AfterReset)
	}
}

func TestDBInitCorruptionAutoRecovery(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "corrupt_test.db")

	// 1. Create a completely corrupted file simulating disk corruption
	err := os.WriteFile(dbPath, []byte("THIS IS COMPLETE GARBAGE DATA NOT A SQLITE DB"), 0644)
	if err != nil {
		t.Fatalf("Failed to write mock corrupted db: %v", err)
	}

	// 2. InitDB should detect corruption and auto-recover cleanly
	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Expected InitDB to auto-recover from corrupted file, but got error: %v", err)
	}
	defer database.Close()

	// 3. Verify the recovered database is healthy and can run queries
	personas, err := database.ListPersonas("test_user")
	if err != nil {
		t.Fatalf("ListPersonas on recovered database failed: %v", err)
	}
	if len(personas) == 0 {
		t.Errorf("Expected seeded personas in recovered database, got 0")
	}
}

