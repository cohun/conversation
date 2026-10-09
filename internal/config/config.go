package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Port            string
	DBPath          string
	GeminiAPIKey    string
	GeminiLiveModel string
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
	// Automatically load .env file if it exists in the working directory
	loadEnvFile(".env")

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

	return &Config{
		Port:            port,
		DBPath:          dbPath,
		GeminiAPIKey:    apiKey,
		GeminiLiveModel: liveModel,
	}
}
