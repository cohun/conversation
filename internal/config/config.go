package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Port               string
	DBPath             string
	GeminiAPIKey       string
	GeminiLiveModel    string
	AdminEmails        []string
	GoogleClientID     string
	GoogleClientSecret string
	AppURL             string
	SessionSecret      string
}

func (c *Config) IsAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, admin := range c.AdminEmails {
		if strings.ToLower(strings.TrimSpace(admin)) == email {
			return true
		}
	}
	return false
}

// loadEnvFile reads a .env file and loads environment variables if not already set
func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return // File does not exist, ignore silently
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			// Strip surrounding quotes if present
			if len(v) >= 2 {
				if (strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`)) ||
					(strings.HasPrefix(v, `'`) && strings.HasSuffix(v, `'`)) {
					v = v[1 : len(v)-1]
				}
			}
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}

func LoadConfig() *Config {
	// Automatically load .env file if it exists in current or parent directory
	loadEnvFile(".env")
	loadEnvFile("../.env")
	loadEnvFile("../../.env")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/app.db"
	}

	apiKey := os.Getenv("GEMINI_API_KEY")

	liveModel := os.Getenv("GEMINI_LIVE_MODEL")
	if liveModel == "" {
		liveModel = "auto"
	}

	// Default primary administrators that are always active admins even if env vars are unset
	defaultAdmins := []string{
		"attila.hitb@gmail.com",
		"attila.private@gmail.com",
		"colin.hunwald@gmail.com",
	}
	adminMap := make(map[string]bool)
	var adminEmails []string
	for _, a := range defaultAdmins {
		if !adminMap[a] {
			adminMap[a] = true
			adminEmails = append(adminEmails, a)
		}
	}

	adminEnv := os.Getenv("ADMIN_EMAILS")
	if adminEnv == "" {
		adminEnv = os.Getenv("ADMIN_EMAIL")
	}
	if adminEnv != "" {
		parts := strings.Split(adminEnv, ",")
		for _, p := range parts {
			trimmed := strings.ToLower(strings.TrimSpace(p))
			if trimmed != "" && !adminMap[trimmed] {
				adminMap[trimmed] = true
				adminEmails = append(adminEmails, trimmed)
			}
		}
	}

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:" + port
	}
	appURL = strings.TrimSuffix(appURL, "/")

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = "antigravity-english-teacher-session-secret"
	}

	return &Config{
		Port:               port,
		DBPath:             dbPath,
		GeminiAPIKey:       apiKey,
		GeminiLiveModel:    liveModel,
		AdminEmails:        adminEmails,
		GoogleClientID:     googleClientID,
		GoogleClientSecret: googleClientSecret,
		AppURL:             appURL,
		SessionSecret:      sessionSecret,
	}
}
