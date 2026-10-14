package ws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"english-teacher-app/internal/auth"
	"english-teacher-app/internal/config"
	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev and Cloud Run
	},
	ReadBufferSize:  1024 * 64,
	WriteBufferSize: 1024 * 64,
}

type SafeWebSocket struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *SafeWebSocket) WriteJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *SafeWebSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

type Hub struct {
	cfg         *config.Config
	db          *db.DB
	resolver    *gemini.ModelResolver
	authHandler *auth.Handler
	sttModelMu  sync.RWMutex
	sttModel    string
	httpClient  *http.Client
}

func NewHub(cfg *config.Config, database *db.DB, resolver *gemini.ModelResolver, authHandler *auth.Handler) *Hub {
	return &Hub{
		cfg:         cfg,
		db:          database,
		resolver:    resolver,
		authHandler: authHandler,
		httpClient: &http.Client{
			Timeout: 6 * time.Second,
		},
	}
}

// ClientInMessage represents messages received from client browser
type ClientInMessage struct {
	Type string `json:"type"`           // "audio", "user_speech", "pass_turn", "bookmark", "interrupt"
	Data string `json:"data,omitempty"` // Base64 16kHz PCM audio
	Text string `json:"text,omitempty"` // Transcribed user speech
}

// ClientOutMessage represents messages sent to client browser
type ClientOutMessage struct {
	Type    string      `json:"type"` // "connected", "audio", "speech", "interrupted", "turn_complete", "topic_saved", "error"
	Data    string      `json:"data,omitempty"`
	Text    string      `json:"text,omitempty"`
	Model   string      `json:"model,omitempty"`
	Persona string      `json:"persona,omitempty"`
	Message string      `json:"message,omitempty"`
	Topic   interface{} `json:"topic,omitempty"`
}

func (h *Hub) HandleLiveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Hub] WebSocket upgrade failed: %v", err)
		return
	}
	clientWS := &SafeWebSocket{conn: conn}
	defer clientWS.Close()

	// 1. Authenticate user
	user := h.authHandler.GetUserFromRequest(r)
	if user == nil {
		// Fallback for initial local development if no sessions exist yet
		if len(h.cfg.AdminEmails) > 0 {
			user, _ = h.db.GetUserByEmail(h.cfg.AdminEmails[0])
		}
	}

	if user == nil {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: "A hívás indításához kérlek jelentkezz be a fiókodba.",
		})
		return
	}

	if user.Status == "blocked" {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: "A fiókodat az adminisztrátor felfüggesztette.",
		})
		return
	}

	if user.Status == "pending" {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: "A regisztrációd jóváhagyásra vár az adminisztrátornál. Hamarosan engedélyezésre kerül!",
		})
		return
	}

	// 2. Check Quota & Custom Key
	allowed, usedMin, limitMin, isCustomKey, customKey, err := h.db.CheckUserQuota(user.ID)
	if err != nil {
		log.Printf("[Hub] Notice: Quota check error for user %s: %v", user.ID, err)
	}
	if !allowed {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: fmt.Sprintf("Mára elfogyott a napi %d perces kereted (%d perc felhasználva). Add meg a saját ingyenes Google kulcsodat a Profilban a korlátlan folytatáshoz!", limitMin, usedMin),
		})
		return
	}

	q := r.URL.Query()
	personaID := q.Get("persona_id")
	resumeID := q.Get("resume_id")
	clientKey := q.Get("api_key")

	// 3. Determine effective API key (BYOK prioritized)
	apiKey := h.cfg.GeminiAPIKey
	usedCustomKey := false
	if isCustomKey && customKey != "" {
		apiKey = customKey
		usedCustomKey = true
	} else if clientKey != "" {
		apiKey = clientKey
		usedCustomKey = true
	} else if settingKey, err := h.db.GetSetting("gemini_api_key"); err == nil && settingKey != "" {
		apiKey = settingKey
	}

	if apiKey == "" {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: "Gemini API Key is not configured. Please set GEMINI_API_KEY environment variable or enter your own key in Profile.",
		})
		return
	}

	// 4. Load Persona
	var targetPersona *db.Persona
	if personaID != "" {
		p, err := h.db.GetPersonaForUser(personaID, user.ID)
		if err == nil {
			targetPersona = p
		}
	}
	if targetPersona == nil {
		// Fallback to default persona (Sarah)
		personas, err := h.db.ListPersonas(user.ID)
		if err == nil && len(personas) > 0 {
			targetPersona = &personas[0]
		} else {
			targetPersona = &db.Persona{
				ID:           "teacher-sarah",
				Name:         "Sarah",
				VoiceName:    "Aoede",
				Mode:         "teacher",
				SystemPrompt: "You are Sarah, a warm and encouraging English teacher.",
			}
		}
	}

	// 5. Load User Profile
	profile, err := h.db.GetUserProfile(user.ID)
	if err != nil {
		profile = &db.UserProfile{
			UserID:       user.ID,
			DisplayName:  "Student",
			PhoneticNote: "Student",
		}
	}

	// 6. Load Resume Conversation if any
	var resumeConv *db.SavedConversation
	if resumeID != "" {
		if c, err := h.db.GetConversation(resumeID); err == nil {
			resumeConv = c
			_ = h.db.UpdateConversationResumed(resumeID)
		}
	}

	// 7. Track call duration for logging & quotas
	callStartTime := time.Now()
	defer func() {
		durSec := int(time.Since(callStartTime).Seconds())
		if durSec >= 5 && targetPersona != nil {
			_ = h.db.LogCall(&db.CallLog{
				UserID:          user.ID,
				PersonaID:       targetPersona.ID,
				StartedAt:       callStartTime,
				EndedAt:         time.Now(),
				DurationSeconds: durSec,
				UsedCustomKey:   usedCustomKey,
			})
		}
	}()

	// 8. Connect to Gemini Live with dynamic fallback chain
	candidateModels := h.resolver.GetCandidateModels(apiKey)
	var geminiWS *SafeWebSocket
	var activeModel string
	var lastErr error

	for _, modelCandidate := range candidateModels {
		log.Printf("[Hub] Attempting connection to Gemini Live with model: %s", modelCandidate)
		gWS, err := h.connectToGeminiLive(modelCandidate, apiKey, targetPersona, profile, resumeConv)
		if err == nil {
			geminiWS = gWS
			activeModel = modelCandidate
			break
		}
		log.Printf("[Hub] Failed connection with model %s: %v, trying next candidate...", modelCandidate, err)
		lastErr = err
	}

	if geminiWS == nil {
		log.Printf("[Hub] Gemini Live WebSocket endpoint restricted on this project/tier (%v). Activating Conversational Voice Engine with gemini-flash-latest...", lastErr)
		h.handleVoiceEngineSession(clientWS, targetPersona, profile, resumeConv, apiKey)
		return
	}

	session := newRollingLiveSession(
		h,
		clientWS,
		targetPersona,
		profile,
		resumeConv,
		apiKey,
		activeModel,
		geminiWS,
	)
	session.Run()
}

// connectToGeminiLive dials the Gemini Live WebSocket endpoint and performs the Setup handshake
func (h *Hub) connectToGeminiLive(
	model string,
	apiKey string,
	persona *db.Persona,
	profile *db.UserProfile,
	resume *db.SavedConversation,
) (*SafeWebSocket, error) {
	return h.connectToGeminiLiveWithContinuation(model, apiKey, persona, profile, resume, "")
}

func (h *Hub) connectToGeminiLiveWithContinuation(
	model string,
	apiKey string,
	persona *db.Persona,
	profile *db.UserProfile,
	resume *db.SavedConversation,
	continuationContext string,
) (*SafeWebSocket, error) {
	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		apiKey,
	)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(liveURL, nil)
	if err != nil {
		status := "unknown"
		if resp != nil {
			status = resp.Status
		}
		return nil, fmt.Errorf("dial failed (HTTP status %s): %w", status, err)
	}

	safeWS := &SafeWebSocket{conn: conn}

	// Prepare system instruction
	systemText := persona.SystemPrompt

	// Add User Profile & Pronunciation Context
	systemText += fmt.Sprintf(
		"\n\n[Student Profile & Pronunciation Context]\nName: %s\nPronunciation Note: %s\nInstruction: Address the student warmly by this name, adhering to their pronunciation preferences.",
		profile.DisplayName,
		profile.PhoneticNote,
	)

	// Add Hands-Free Spoken Interaction Instruction
	systemText += "\n\n[Hands-Free Spoken Interaction & Turn-Taking]\n" +
		"The student is practicing English while driving or hands-free. They may take long pauses (several seconds) to think, hesitate, or formulate sentences. NEVER interrupt them mid-thought!\n" +
		"The student concludes their turn by saying 'Your turn!' (or 'Over to you!', 'What do you think?').\n" +
		"When you receive the turn, respond promptly, warmly, and concisely without delay."

	// Add Resume Context if applicable
	if resume != nil {
		if continuationContext == "" {
			systemText += fmt.Sprintf(
				"\n\n[Resuming Saved Topic]\nYou are resuming an earlier conversation with %s.\nTopic Title: %s\nSummary of prior discussion: %s\nKey Points/Vocabulary: %s\nLast Open Question: %s\nInstruction: Start the call with a warm welcome acknowledging this topic and invite their thoughts on the open question.",
				profile.DisplayName,
				resume.TopicTitle,
				resume.Summary,
				resume.KeyPoints,
				resume.OpenQuestion,
			)
		} else {
			systemText += fmt.Sprintf(
				"\n\n[Resuming Saved Topic Background]\nTopic Title: %s\nSummary: %s\nKey Points: %s",
				resume.TopicTitle,
				resume.Summary,
				resume.KeyPoints,
			)
		}
	}

	if continuationContext != "" {
		systemText += "\n\n" + continuationContext
	}

	voiceName := persona.VoiceName
	if voiceName == "" {
		voiceName = "Aoede"
	}

	// Build Setup Message
	setupMsg := gemini.LiveClientMessage{
		Setup: &gemini.LiveSetup{
			Model: model,
			GenerationConfig: &gemini.LiveGenerationConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &gemini.LiveSpeechConfig{
					VoiceConfig: &gemini.LiveVoiceConfig{
						PrebuiltVoiceConfig: &gemini.LivePrebuiltVoiceConfig{
							VoiceName: voiceName,
						},
					},
				},
			},
			InputAudioTranscription:  &gemini.LiveAudioTranscriptionConfig{},
			OutputAudioTranscription: &gemini.LiveAudioTranscriptionConfig{},
			SystemInstruction: &gemini.LiveContent{
				Parts: []gemini.LivePart{
					{Text: systemText},
				},
			},
			Tools: []gemini.LiveTool{
				{
					FunctionDeclarations: []gemini.LiveFunctionDeclaration{
						{
							Name:        "save_conversation",
							Description: "Saves the current conversation topic, summary, key vocabulary points, and open question for later resumption.",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"topic_title": map[string]interface{}{
										"type":        "string",
										"description": "Short expressive title of the topic discussed",
									},
									"summary": map[string]interface{}{
										"type":        "string",
										"description": "2-3 sentences concise summary of what was discussed",
									},
									"key_points": map[string]interface{}{
										"type":        "string",
										"description": "Key concepts, grammar rules, or new vocabulary words",
									},
									"open_question": map[string]interface{}{
										"type":        "string",
										"description": "The last open or unanswered question",
									},
								},
								"required": []string{"topic_title", "summary"},
							},
						},
					},
				},
			},
		},
	}

	// Send Setup Message
	if err := safeWS.WriteJSON(setupMsg); err != nil {
		safeWS.Close()
		return nil, fmt.Errorf("failed to send setup message: %w", err)
	}

	// Read setupComplete
	_ = safeWS.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, firstRaw, err := safeWS.conn.ReadMessage()
	_ = safeWS.conn.SetReadDeadline(time.Time{})
	if err != nil {
		safeWS.Close()
		return nil, fmt.Errorf("timeout or error waiting for setupComplete: %w", err)
	}

	var firstResp gemini.LiveServerMessage
	if err := json.Unmarshal(firstRaw, &firstResp); err != nil {
		safeWS.Close()
		return nil, fmt.Errorf("failed to parse setup response: %w", err)
	}

	if firstResp.SetupComplete == nil {
		safeWS.Close()
		return nil, fmt.Errorf("gemini live setup failed (received: %s)", string(firstRaw))
	}
	log.Printf("[Hub] Received setupComplete from Gemini Live for model %s", model)

	return safeWS, nil
}

// DetectVoiceTriggerLive uses a short-lived helper BidiGenerateContent WebSocket session
// to transcribe a 3.5s tail PCM clip and detect turn-handover phrases ("Your turn!", "Over to you!", etc.)
// without disturbing the main Sarah conversation WebSocket during student pauses.
func (h *Hub) DetectVoiceTriggerLive(ctx context.Context, apiKey, model string, normPCM []byte) (bool, string, error) {
	if len(normPCM) < 3200 || apiKey == "" {
		return false, "", nil
	}
	if model == "" || model == "auto" {
		model = "models/gemini-3.8-live"
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		apiKey,
	)

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.DialContext(checkCtx, liveURL, nil)
	if err != nil {
		// Fallback to REST transcription if WebSocket dial fails
		wavBytes := encodePCM16ToWAV(normPCM, 16000)
		text, restErr := h.TranscribeAudioClip(checkCtx, apiKey, wavBytes)
		return false, text, restErr
	}
	defer conn.Close()

	setupMsg := gemini.LiveClientMessage{
		Setup: &gemini.LiveSetup{
			Model: model,
			GenerationConfig: &gemini.LiveGenerationConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &gemini.LiveSpeechConfig{
					VoiceConfig: &gemini.LiveVoiceConfig{
						PrebuiltVoiceConfig: &gemini.LivePrebuiltVoiceConfig{
							VoiceName: "Aoede",
						},
					},
				},
			},
			InputAudioTranscription:  &gemini.LiveAudioTranscriptionConfig{},
			OutputAudioTranscription: &gemini.LiveAudioTranscriptionConfig{},
			SystemInstruction: &gemini.LiveContent{
				Parts: []gemini.LivePart{
					{
						Text: "You are a verbatim speech-to-text transcriber and turn-handover detector. " +
							"Repeat ONLY the exact English words spoken by the user in the audio clip word-for-word, with zero extra commentary. " +
							"If the user explicitly speaks a turn-handover phrase ('your turn', 'over to you', 'go ahead', or 'what do you think'), call the trigger_turn_handover tool immediately.",
					},
				},
			},
			Tools: []gemini.LiveTool{
				{
					FunctionDeclarations: []gemini.LiveFunctionDeclaration{
						{
							Name:        "trigger_turn_handover",
							Description: "Call immediately when the user speaks 'your turn', 'over to you', 'go ahead', or 'what do you think'.",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"phrase": map[string]interface{}{
										"type":        "string",
										"description": "The exact handover phrase spoken by the user",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := conn.WriteJSON(setupMsg); err != nil {
		return false, "", err
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var setupResp gemini.LiveServerMessage
	if err := conn.ReadJSON(&setupResp); err != nil || setupResp.SetupComplete == nil {
		return false, "", fmt.Errorf("trigger detector setup failed: %v", err)
	}

	// Stream the normalized tail PCM in 100ms (3200-byte) chunks
	const chunkSize = 3200
	for offset := 0; offset < len(normPCM); offset += chunkSize {
		end := offset + chunkSize
		if end > len(normPCM) {
			end = len(normPCM)
		}
		b64 := base64.StdEncoding.EncodeToString(normPCM[offset:end])
		chunkMsg := gemini.LiveClientMessage{
			RealtimeInput: &gemini.LiveRealtimeInput{
				MediaChunks: []gemini.LiveBlob{
					{
						MimeType: "audio/pcm;rate=16000",
						Data:     b64,
					},
				},
			},
		}
		if err := conn.WriteJSON(chunkMsg); err != nil {
			return false, "", err
		}
	}

	commitMsg := gemini.LiveClientMessage{
		ClientContent: &gemini.LiveClientContent{
			TurnComplete: true,
		},
	}
	if err := conn.WriteJSON(commitMsg); err != nil {
		return false, "", err
	}

	var inputTranscript strings.Builder
	var outputTranscript strings.Builder
	deadline := time.Now().Add(4500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
		var msg gemini.LiveServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		if msg.ToolCall != nil {
			for _, fc := range msg.ToolCall.FunctionCalls {
				if fc.Name == "trigger_turn_handover" {
					phrase, _ := fc.Args["phrase"].(string)
					if phrase == "" {
						phrase = "your turn"
					}
					combined := strings.TrimSpace(inputTranscript.String() + " " + outputTranscript.String() + " " + phrase)
					return true, combined, nil
				}
			}
		}

		if msg.ServerContent != nil {
			if msg.ServerContent.InputTranscription != nil && msg.ServerContent.InputTranscription.Text != "" {
				inputTranscript.WriteString(msg.ServerContent.InputTranscription.Text)
			} else if msg.ServerContent.InterimInputTranscription != nil && msg.ServerContent.InterimInputTranscription.Text != "" {
				inputTranscript.WriteString(msg.ServerContent.InterimInputTranscription.Text)
			}
			if msg.ServerContent.OutputTranscription != nil && msg.ServerContent.OutputTranscription.Text != "" {
				outputTranscript.WriteString(msg.ServerContent.OutputTranscription.Text)
			}
			if msg.ServerContent.ModelTurn != nil {
				for _, p := range msg.ServerContent.ModelTurn.Parts {
					if p.Text != "" {
						outputTranscript.WriteString(p.Text)
					}
				}
			}

			// Early exit as soon as either input or output transcription contains a voice trigger
			inStr := strings.TrimSpace(inputTranscript.String())
			outStr := strings.TrimSpace(outputTranscript.String())
			if MatchesVoiceTriggerText(inStr) {
				return true, inStr, nil
			}
			if MatchesVoiceTriggerText(outStr) {
				return true, outStr, nil
			}

			if msg.ServerContent.TurnComplete {
				break
			}
		}
	}

	finalText := strings.TrimSpace(inputTranscript.String())
	if finalText == "" {
		finalText = strings.TrimSpace(outputTranscript.String())
	}
	return MatchesVoiceTriggerText(finalText), finalText, nil
}

// TranscribeAudioClip transcribes a short 16kHz 16-bit mono WAV clip using Gemini Flash REST API
// for fast (<500ms) server-side detection of the 'Your turn!' voice trigger.
func (h *Hub) TranscribeAudioClip(ctx context.Context, apiKey string, wavBytes []byte) (string, error) {
	if len(wavBytes) <= 44 || apiKey == "" {
		return "", nil
	}

	b64Wav := base64.StdEncoding.EncodeToString(wavBytes)
	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{
						"inlineData": map[string]interface{}{
							"mimeType": "audio/wav",
							"data":     b64Wav,
						},
					},
					{
						"text": "Transcribe the spoken English words in this audio clip verbatim. Output ONLY the exact spoken words in plain text, or empty string if there is no speech.",
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.0,
			"maxOutputTokens": 40,
		},
	}

	jsonPayload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	defaultCandidates := []string{
		"gemini-3.8-flash",
		"gemini-flash-latest",
		"gemini-3.5-flash",
		"gemini-2.5-flash",
		"gemini-2.0-flash",
	}

	h.sttModelMu.RLock()
	cached := h.sttModel
	h.sttModelMu.RUnlock()

	var candidates []string
	if cached != "" {
		candidates = append(candidates, cached)
	}
	for _, m := range defaultCandidates {
		if m != cached {
			candidates = append(candidates, m)
		}
	}

	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	for _, modelName := range candidates {
		reqCtx, cancel := context.WithTimeout(ctx, 4500*time.Millisecond)
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)
		httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonPayload))
		if reqErr != nil {
			cancel()
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, doErr := client.Do(httpReq)
		if doErr != nil {
			cancel()
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			cancel()
			continue
		}

		var result struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		cancel()

		if decErr == nil {
			if cached != modelName {
				h.sttModelMu.Lock()
				h.sttModel = modelName
				h.sttModelMu.Unlock()
			}
			if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
				return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text), nil
			}
			return "", nil
		}
	}

	return "", fmt.Errorf("all flash transcription models failed")
}

func (h *Hub) handleSaveConversationTool(
	geminiWS *SafeWebSocket,
	clientWS *SafeWebSocket,
	personaID string,
	call gemini.LiveFunctionCall,
) {
	topicTitle, _ := call.Args["topic_title"].(string)
	if topicTitle == "" {
		topicTitle = "Conversation " + time.Now().Format("Jan 02 15:04")
	}

	summary, _ := call.Args["summary"].(string)
	if summary == "" {
		summary = "Spoken conversation session."
	}

	keyPoints, _ := call.Args["key_points"].(string)
	openQuestion, _ := call.Args["open_question"].(string)

	conv := &db.SavedConversation{
		ID:           "topic-" + uuid.New().String()[:8],
		UserID:       "default_user",
		PersonaID:    personaID,
		TopicTitle:   topicTitle,
		Summary:      summary,
		KeyPoints:    keyPoints,
		OpenQuestion: openQuestion,
	}

	if err := h.db.SaveConversation(conv); err != nil {
		log.Printf("[Hub] Error saving conversation to DB: %v", err)
	} else {
		log.Printf("[Hub] Saved conversation '%s' (%s) to DB", conv.TopicTitle, conv.ID)
	}

	// Send ToolResponse back to Gemini so it can verbally acknowledge
	toolResponse := gemini.LiveClientMessage{
		ToolResponse: &gemini.LiveToolResponse{
			FunctionResponses: []gemini.LiveFunctionResponse{
				{
					ID: call.ID,
					Response: map[string]interface{}{
						"output": map[string]interface{}{
							"status":      "success",
							"topic_title": conv.TopicTitle,
							"saved_id":    conv.ID,
						},
					},
				},
			},
		},
	}
	_ = geminiWS.WriteJSON(toolResponse)

	// Notify client browser
	_ = clientWS.WriteJSON(ClientOutMessage{
		Type:  "topic_saved",
		Topic: conv,
	})
}

// handleVoiceEngineSession provides seamless conversational voice interaction when Live WebSocket is restricted
func (h *Hub) handleVoiceEngineSession(
	clientWS *SafeWebSocket,
	persona *db.Persona,
	profile *db.UserProfile,
	resume *db.SavedConversation,
	apiKey string,
) {
	activeModel := "gemini-3.8-flash"

	// 1. Notify browser of successful connection
	_ = clientWS.WriteJSON(ClientOutMessage{
		Type:    "connected",
		Model:   activeModel + " (Conversational Voice Engine)",
		Persona: persona.Name,
	})

	// Build System Prompt
	systemText := persona.SystemPrompt
	systemText += fmt.Sprintf(
		"\n\n[Student Profile & Pronunciation Context]\nName: %s\nPronunciation Note: %s\nInstruction: Address the student warmly by this name, respecting their pronunciation preferences. Keep your spoken turns natural, conversational, and direct for a voice telephone call.",
		profile.DisplayName,
		profile.PhoneticNote,
	)

	if resume != nil {
		systemText += fmt.Sprintf(
			"\n\n[Resuming Saved Topic]\nYou are resuming an earlier conversation with %s.\nTopic Title: %s\nSummary of prior discussion: %s\nKey Points/Vocabulary: %s\nLast Open Question: %s\nInstruction: Start the call with a warm welcome acknowledging this topic and invite their thoughts on the open question.",
			profile.DisplayName,
			resume.TopicTitle,
			resume.Summary,
			resume.KeyPoints,
			resume.OpenQuestion,
		)
	}

	type ChatContent struct {
		Role  string                   `json:"role"`
		Parts []map[string]interface{} `json:"parts"`
	}

	var history []ChatContent
	var mu sync.Mutex

	candidateVoiceModels := []string{
		"gemini-3.8-flash",
		"gemini-3.5-flash",
		"gemini-flash-latest",
		"gemini-3.7-flash",
	}

	callGemini := func(userMessage string) (string, error) {
		mu.Lock()
		history = append(history, ChatContent{
			Role:  "user",
			Parts: []map[string]interface{}{{"text": userMessage}},
		})
		currentHistory := make([]ChatContent, len(history))
		copy(currentHistory, history)
		mu.Unlock()

		reqBody := map[string]interface{}{
			"contents": currentHistory,
			"systemInstruction": map[string]interface{}{
				"parts": []map[string]interface{}{{"text": systemText}},
			},
		}

		jsonPayload, err := json.Marshal(reqBody)
		if err != nil {
			return "", err
		}

		for _, modelName := range candidateVoiceModels {
			url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)
			httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonPayload))
			if err != nil {
				continue
			}
			httpReq.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(httpReq)
			if err != nil {
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}

			var result struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}

			err = json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()

			if err == nil && len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
				aiText := result.Candidates[0].Content.Parts[0].Text
				mu.Lock()
				history = append(history, ChatContent{
					Role:  "model",
					Parts: []map[string]interface{}{{"text": aiText}},
				})
				mu.Unlock()
				return aiText, nil
			}
		}

		return "I hear you! Let's continue our conversation.", nil
	}

	// Initial greeting from AI
	go func() {
		initialPrompt := fmt.Sprintf("Hello %s! The phone call has just connected. Greet me warmly by my name (%s), introduce yourself briefly, and invite me to start our spoken session.", persona.Name, profile.DisplayName)
		if resume != nil {
			initialPrompt = fmt.Sprintf("We are resuming our discussion about '%s'. Please greet me warmly and ask me about the open question: '%s'", resume.TopicTitle, resume.OpenQuestion)
		}
		greeting, err := callGemini(initialPrompt)
		if err == nil && greeting != "" {
			_ = clientWS.WriteJSON(ClientOutMessage{
				Type: "speech",
				Text: greeting,
			})
		}
	}()

	// Read loop from client
	for {
		var clientMsg ClientInMessage
		if err := clientWS.conn.ReadJSON(&clientMsg); err != nil {
			log.Printf("[Hub] Voice Engine client read closed: %v", err)
			return
		}

		switch clientMsg.Type {
		case "user_speech":
			if strings.TrimSpace(clientMsg.Text) != "" {
				go func(userText string) {
					aiResponse, err := callGemini(userText)
					if err == nil {
						_ = clientWS.WriteJSON(ClientOutMessage{
							Type: "speech",
							Text: aiResponse,
						})
					}
				}(clientMsg.Text)
			}

		case "pass_turn":
			go func(userText string) {
				prompt := strings.TrimSpace(userText)
				if prompt == "" {
					prompt = "[The student finished speaking and handed the turn to you. Please proceed with your response following your cycle.]"
				}
				aiResponse, err := callGemini(prompt)
				if err == nil {
					_ = clientWS.WriteJSON(ClientOutMessage{
						Type: "speech",
						Text: aiResponse,
					})
				}
			}(clientMsg.Text)

		case "bookmark":
			go func() {
				title := "Topic " + time.Now().Format("Jan 02 15:04")
				summary := "Spoken practice session with " + persona.Name
				conv := &db.SavedConversation{
					ID:           "topic-" + uuid.New().String()[:8],
					UserID:       "default_user",
					PersonaID:    persona.ID,
					TopicTitle:   title,
					Summary:      summary,
					KeyPoints:    "Spoken English dialogue practice",
					OpenQuestion: "What topic would you like to explore next?",
				}
				_ = h.db.SaveConversation(conv)
				_ = clientWS.WriteJSON(ClientOutMessage{
					Type:  "topic_saved",
					Topic: conv,
				})
				_ = clientWS.WriteJSON(ClientOutMessage{
					Type: "speech",
					Text: fmt.Sprintf("I've saved our conversation about %s. We can pick it up anytime!", title),
				})
			}()

		case "interrupt", "client_interrupt":
			_ = clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})
		}
	}
}
