package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"english-teacher-app/internal/auth"
	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"
	"english-teacher-app/internal/ws"
)

type ringLogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newRingLogBuffer(max int) *ringLogBuffer {
	return &ringLogBuffer{max: max, lines: make([]string, 0, max)}
}

func (r *ringLogBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := strings.TrimRight(string(p), "\r\n")
	if len(r.lines) >= r.max {
		r.lines = r.lines[1:]
	}
	r.lines = append(r.lines, msg)
	return len(p), nil
}

func (r *ringLogBuffer) GetAll() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]string, len(r.lines))
	copy(cp, r.lines)
	return cp
}

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

func getEffectiveUserID(r *http.Request, authHandler *auth.Handler) string {
	user := authHandler.GetUserFromRequest(r)
	if user != nil {
		return user.ID
	}
	return "default_user"
}

func checkUserActive(r *http.Request, authHandler *auth.Handler) error {
	user := authHandler.GetUserFromRequest(r)
	if user == nil {
		return fmt.Errorf("A művelethez bejelentkezés szükséges.")
	}
	if user.Status == "pending" {
		return fmt.Errorf("A fiókod jóváhagyásra vár a rendszergazdánál.")
	}
	if user.Status == "blocked" {
		return fmt.Errorf("A fiókodat az adminisztrátor felfüggesztette.")
	}
	return nil
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

	logRing := newRingLogBuffer(300)
	log.SetOutput(io.MultiWriter(os.Stderr, logRing))

	log.Printf("[Server] Initializing database at %s...", cfg.DBPath)
	database, err := db.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("[Server] Failed to initialize database: %v", err)
	}
	defer database.Close()

	resolver := gemini.NewModelResolver(database, cfg.GeminiLiveModel)
	authHandler := auth.NewHandler(cfg, database, resolver)

	// Bootstrap configured admin accounts
	if len(cfg.AdminEmails) > 0 {
		if err := database.EnsureAdminEmails(cfg.AdminEmails); err != nil {
			log.Printf("[Server] Notice: EnsureAdminEmails error: %v", err)
		} else {
			log.Printf("[Server] ✅ Admin accounts configured for: %v", cfg.AdminEmails)
		}
	}

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

	hub := ws.NewHub(cfg, database, resolver, authHandler)

	mux := http.NewServeMux()

	// WebSocket Live Endpoint
	mux.HandleFunc("/ws/live", hub.HandleLiveWS)

	// Auth Endpoints
	mux.HandleFunc("POST /api/auth/register", authHandler.HandleRegister)
	mux.HandleFunc("POST /api/auth/login", authHandler.HandleLogin)
	mux.HandleFunc("POST /api/auth/logout", authHandler.HandleLogout)
	mux.HandleFunc("GET /api/auth/me", authHandler.HandleGetMe)
	mux.HandleFunc("GET /api/auth/google/login", authHandler.HandleGoogleLogin)
	mux.HandleFunc("GET /api/auth/google/callback", authHandler.HandleGoogleCallback)
	mux.HandleFunc("POST /api/user/custom-key", authHandler.HandleSetCustomKey)

	// Admin Endpoints
	mux.HandleFunc("GET /api/admin/users", authHandler.RequireAdmin(authHandler.HandleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users/status", authHandler.RequireAdmin(authHandler.HandleAdminUpdateUserStatus))
	mux.HandleFunc("POST /api/admin/users/limit", authHandler.RequireAdmin(authHandler.HandleAdminUpdateUserLimit))
	mux.HandleFunc("DELETE /api/admin/users", authHandler.RequireAdmin(authHandler.HandleAdminDeleteUser))

	// REST API: User Profile
	mux.HandleFunc("GET /api/profile", func(w http.ResponseWriter, r *http.Request) {
		userID := getEffectiveUserID(r, authHandler)
		p, err := database.GetUserProfile(userID)
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

	mux.HandleFunc("POST /api/profile", func(w http.ResponseWriter, r *http.Request) {
		userID := getEffectiveUserID(r, authHandler)
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

		if err := database.UpdateUserProfile(userID, req.DisplayName, req.PhoneticNote); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to update profile: "+err.Error())
			return
		}

		p, err := database.GetUserProfile(userID)
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
		userID := getEffectiveUserID(r, authHandler)
		list, err := database.ListPersonas(userID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to list personas: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /api/personas", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUserActive(r, authHandler); err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
		userID := getEffectiveUserID(r, authHandler)
		var p db.Persona
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.SystemPrompt) == "" {
			jsonError(w, http.StatusBadRequest, "name and system_prompt are required")
			return
		}
		p.UserID = userID
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

	mux.HandleFunc("PUT /api/personas/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUserActive(r, authHandler); err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
		userID := getEffectiveUserID(r, authHandler)
		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/personas/")
		}
		var p db.Persona
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		p.ID = id
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

		existing, err := database.GetPersona(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "Companion nem található")
			return
		}

		if existing.IsDefault {
			// This is a default companion (Sarah, Fred, Maya). Save as per-user override ONLY!
			if err := database.SaveUserPersonaOverride(userID, id, p.Name, p.ShortDesc, p.SystemPrompt, p.VoiceName, p.Mode); err != nil {
				jsonError(w, http.StatusInternalServerError, "Failed to save persona override: "+err.Error())
				return
			}
			updated, err := database.GetPersonaForUser(id, userID)
			if err != nil {
				p.IsDefault = true
				p.HasOverride = true
				jsonResponse(w, http.StatusOK, p)
				return
			}
			jsonResponse(w, http.StatusOK, updated)
			return
		}

		// Custom persona: check ownership or admin
		if existing.UserID != userID && existing.UserID != "default_user" {
			user := authHandler.GetUserFromRequest(r)
			if user == nil || user.Role != "admin" {
				jsonError(w, http.StatusForbidden, "Nincs jogosultságod más felhasználó companion-jának szerkesztésére")
				return
			}
		}

		if err := database.UpdatePersona(&p); err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to update persona: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, p)
	})

	mux.HandleFunc("POST /api/personas/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUserActive(r, authHandler); err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
		userID := getEffectiveUserID(r, authHandler)
		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/personas/")
			id = strings.TrimSuffix(id, "/reset")
		}
		if id == "" {
			jsonError(w, http.StatusBadRequest, "persona id is required")
			return
		}

		if err := database.ResetUserPersonaOverride(userID, id); err != nil {
			jsonError(w, http.StatusInternalServerError, "Nem sikerült visszaállítani az alapbeállításokat: "+err.Error())
			return
		}

		fresh, err := database.GetPersonaForUser(id, userID)
		if err != nil {
			fresh, _ = database.GetDefaultPersonaTemplate(id)
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"message": "Companion alapbeállításai sikeresen visszaállítva!",
			"persona": fresh,
		})
	})

	mux.HandleFunc("GET /api/personas/{id}/template", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			id = strings.TrimPrefix(r.URL.Path, "/api/personas/")
			id = strings.TrimSuffix(id, "/template")
		}
		tpl, err := database.GetDefaultPersonaTemplate(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "Companion default template not found")
			return
		}
		jsonResponse(w, http.StatusOK, tpl)
	})

	mux.HandleFunc("DELETE /api/personas/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUserActive(r, authHandler); err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
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
		userID := getEffectiveUserID(r, authHandler)
		list, err := database.ListConversations(userID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Failed to list conversations: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /api/conversations", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUserActive(r, authHandler); err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
		userID := getEffectiveUserID(r, authHandler)
		var c db.SavedConversation
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		if strings.TrimSpace(c.TopicTitle) == "" {
			jsonError(w, http.StatusBadRequest, "topic_title is required")
			return
		}
		c.UserID = userID
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

	// Version Info Endpoint
	const AppVersion = "2.4.18-turncomplete-fix"
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"version":          AppVersion,
			"build_time":       time.Now().Format(time.RFC3339),
			"active_model":     resolver.GetEffectiveModel(effectiveAPIKey),
			"audio_output":     "CegAgy Architecture (Mobile Loudspeaker Car-Tuned 2.3x + Broadcast Compressor / Desktop Clean 1.15x)",
			"activity_control": "Gemini Multimodal Live Native TurnComplete + Voice Trigger + 8s Auto-VAD",
			"downsampler":      "Continuous Phase Linear Interpolation (16kHz)",
		})
	})

	// Debug Server Logs: Inspect live server logs in memory
	mux.HandleFunc("GET /api/debug/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, l := range logRing.GetAll() {
			_, _ = fmt.Fprintln(w, l)
		}
	})

	// Debug Test Voice Endpoint: runs an automated live simulated call directly against Gemini Live
	mux.HandleFunc("GET /api/debug/test-voice", func(w http.ResponseWriter, r *http.Request) {
		key := effectiveAPIKey
		if k := r.URL.Query().Get("key"); k != "" {
			key = k
		}
		if key == "" {
			jsonError(w, http.StatusBadRequest, "No Gemini API key available on server")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()

		model := resolver.GetEffectiveModel(key)
		res, err := gemini.TestLiveVoiceConnection(ctx, key, model)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
				"status":  "error",
				"version": AppVersion,
				"error":   err.Error(),
			})
			return
		}
		res["version"] = AppVersion
		jsonResponse(w, http.StatusOK, res)
	})

	// Static Web Assets with aggressive cache-control prevention for JS/HTML
	staticDir := findStaticDir()
	log.Printf("[Server] Serving static files from %s", staticDir)
	fileServer := http.FileServer(http.Dir(staticDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html") || path == "/" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		fileServer.ServeHTTP(w, r)
	})

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
