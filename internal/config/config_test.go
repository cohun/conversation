package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	content := `
# Comment line
TEST_KEY_1=hello_world
TEST_KEY_2="quoted_value"
TEST_KEY_3='single_quoted'
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test .env: %v", err)
	}

	loadEnvFile(envPath)

	if os.Getenv("TEST_KEY_1") != "hello_world" {
		t.Errorf("Expected 'hello_world', got '%s'", os.Getenv("TEST_KEY_1"))
	}
	if os.Getenv("TEST_KEY_2") != "quoted_value" {
		t.Errorf("Expected 'quoted_value', got '%s'", os.Getenv("TEST_KEY_2"))
	}
	if os.Getenv("TEST_KEY_3") != "single_quoted" {
		t.Errorf("Expected 'single_quoted', got '%s'", os.Getenv("TEST_KEY_3"))
	}
}
