package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"
	"english-teacher-app/internal/ws"
)

func maskKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) <= 8 {
		if len(k) == 0 {
			return ""
		}
		return "***"
	}
	return k[:4] + "..." + k[len(k)-4:]
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func findStaticDir() string {
	candidates := []string{
		"web/static",
		"./web/static",
		"/app/web/static",
		"../../web/static",
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "web/static"
}

func main() {
	cfg := config.LoadConfig()

	log.Printf("[Server] Initializing database at %s...", cfg.DBPath)
	database, err := db.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("[Server] Failed to initialize database: %v", err)
	}
	defer database.Close()

	resolver := gemini.NewModelResolver(database, cfg.GeminiLiveModel)

	// Effective API key
	effectiveAPIKey := cfg.GeminiAPIKey
	if effectiveAPIKey == "" {
		if settingKey, err := database.GetSetting("gemini_api_key"); err == nil && settingKey != "" {
			effectiveAPIKey = settingKey
		}
	}

	// Trigger model discovery if API key is available
	if effectiveAPIKey != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := resolver.DiscoverModels(ctx, effectiveAPIKey); err != nil {
				log.Printf("[Server] Notice: Initial dynamic model discovery: %v", err)
			}
		}()
	}

	hub := ws.NewHub(cfg, database, resolver)

	mux := http.NewServeMux()

	// WebSocket Live Endpoint
	mux.HandleFunc("/ws/live", hub.HandleLiveWS)

	// REST API: User Profile
	mux.HandleFunc("GET /api/profile", func(w http.ResponseWriter, r *http.Request) {
		p, err := database.GetUserProfile("default_user")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to load profile: "+err.Error())
			return
		}
		// Return both wrapped and root fields for maximal client compatibility
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"profile":       p,
			"id":            p.ID,
			"user_id":       p.UserID,
			"display_name":  p.DisplayName,
			"phonetic_note": p.PhoneticNote,
			"created_at":    p.CreatedAt,
		})
	})

	mux.HandleFunc("POST /api/profile", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DisplayName  string `json:"display_name"`
			PhoneticNote string `json:"phonetic_note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		if strings.TrimSpace(req.DisplayName) == "" {
			jsonError(w, http.StatusBadRequest, "display_name is required")
			return
		}

		if err := database.UpdateUserProfile("default_user", req.DisplayName, req.PhoneticNote); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to update profile: "+err.Error())
			return
		}

		p, err := database.GetUserProfile("default_user")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to load profile: "+err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"profile":       p,
			"id":            p.ID,
			"user_id":       p.UserID,
			"display_name":  p.DisplayName,
			"phonetic_note": p.PhoneticNote,
			"created_at":    p.CreatedAt,
		})
	})

	// REST API: Personas
	mux.HandleFunc("GET /api/personas", func(w http.ResponseWriter, r *http.Request) {
		list, err := database.ListPersonas("default_user")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to list personas: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /api/personas", func(w http.ResponseWriter, r *http.Request) {
		var p db.Persona
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.SystemPrompt) == "" {
			jsonError(w, http.StatusBadRequest, "name and system_prompt are required")
			return
		}
		if p.Mode == "" {
			p.Mode = "teacher"
		}
		if p.VoiceName == "" {
			p.VoiceName = "Aoede"
		}

		if err := database.CreatePersona(&p); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to create persona: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, p)
	})

	mux.HandleFunc("DELETE /api/personas/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/personas/")
		}
		if id == "" {
			jsonError(w, http.StatusBadRequest, "persona id is required")
			return
		}
		if err := database.DeletePersona(id); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to delete persona: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// REST API: Conversations
	mux.HandleFunc("GET /api/conversations", func(w http.ResponseWriter, r *http.Request) {
		list, err := database.ListConversations("default_user")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to list conversations: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /api/conversations", func(w http.ResponseWriter, r *http.Request) {
		var c db.SavedConversation
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		if strings.TrimSpace(c.TopicTitle) == "" {
			jsonError(w, http.StatusBadRequest, "topic_title is required")
			return
		}
		if err := database.SaveConversation(&c); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to save conversation: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"topic":  c,
		})
	})

	mux.HandleFunc("DELETE /api/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/conversations/")
		}
		if id == "" {
			jsonError(w, http.StatusBadRequest, "conversation id is required")
			return
		}
		if err := database.DeleteConversation(id); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to delete conversation: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// REST API: App Settings
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		key := cfg.GeminiAPIKey
		if key == "" {
			if settingKey, err := database.GetSetting("gemini_api_key"); err == nil && settingKey != "" {
				key = settingKey
			}
		}

		defaultLiveModel := cfg.GeminiLiveModel
		if settingModel, err := database.GetSetting("default_live_model"); err == nil && settingModel != "" {
			defaultLiveModel = settingModel
		}
		if defaultLiveModel == "" {
			defaultLiveModel = "auto"
		}

		activeModel := resolver.GetEffectiveModel(key)

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"has_api_key":        key != "",
			"masked_api_key":     maskKey(key),
			"default_live_model": defaultLiveModel,
			"active_model":       activeModel,
		})
	})

	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DefaultLiveModel string `json:"default_live_model"`
			GeminiAPIKey     string `json:"gemini_api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}

		if req.GeminiAPIKey != "" {
			_ = database.SetSetting("gemini_api_key", req.GeminiAPIKey)
			cfg.GeminiAPIKey = req.GeminiAPIKey
			// Refresh discovery
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = resolver.DiscoverModels(ctx, req.GeminiAPIKey)
			}()
		}

		if req.DefaultLiveModel != "" {
			_ = database.SetSetting("default_live_model", req.DefaultLiveModel)
		}

		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Static Web Assets
	staticDir := findStaticDir()
	log.Printf("[Server] Serving static files from %s", staticDir)
	fileServer := http.FileServer(http.Dir(staticDir))
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  60 * time.Minute, // Support long-lived phone call WebSockets
		WriteTimeout: 60 * time.Minute,
		IdleTimeout:  60 * time.Minute,
	}

	// Graceful shutdown handling
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[Server] 🚀 Server running at http://localhost:%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] ListenAndServe failed: %v", err)
		}
	}()

	<-stopChan
	log.Printf("[Server] Shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Server] Shutdown error: %v", err)
	}
	log.Printf("[Server] Server stopped.")
}
