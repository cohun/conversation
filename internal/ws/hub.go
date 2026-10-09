package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

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
	cfg      *config.Config
	db       *db.DB
	resolver *gemini.ModelResolver
}

func NewHub(cfg *config.Config, database *db.DB, resolver *gemini.ModelResolver) *Hub {
	return &Hub{
		cfg:      cfg,
		db:       database,
		resolver: resolver,
	}
}

// ClientInMessage represents messages received from client browser
type ClientInMessage struct {
	Type string `json:"type"` // "audio", "pass_turn", "bookmark", "interrupt"
	Data string `json:"data"` // Base64 16kHz PCM audio
}

// ClientOutMessage represents messages sent to client browser
type ClientOutMessage struct {
	Type    string      `json:"type"` // "connected", "audio", "interrupted", "turn_complete", "topic_saved", "error"
	Data    string      `json:"data,omitempty"`
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

	q := r.URL.Query()
	personaID := q.Get("persona_id")
	resumeID := q.Get("resume_id")
	clientKey := q.Get("api_key")

	// Determine effective API key
	apiKey := h.cfg.GeminiAPIKey
	if apiKey == "" {
		apiKey = clientKey
	}
	if apiKey == "" {
		if settingKey, err := h.db.GetSetting("gemini_api_key"); err == nil && settingKey != "" {
			apiKey = settingKey
		}
	}

	if apiKey == "" {
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: "Gemini API Key is not configured. Please set GEMINI_API_KEY environment variable or enter it in App Settings.",
		})
		return
	}

	// Load Persona
	var targetPersona *db.Persona
	if personaID != "" {
		p, err := h.db.GetPersona(personaID)
		if err == nil {
			targetPersona = p
		}
	}
	if targetPersona == nil {
		// Fallback to default persona (Sarah)
		personas, err := h.db.ListPersonas("default_user")
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

	// Load User Profile
	profile, err := h.db.GetUserProfile("default_user")
	if err != nil {
		profile = &db.UserProfile{
			DisplayName:  "Student",
			PhoneticNote: "Student",
		}
	}

	// Load Resume Conversation if any
	var resumeConv *db.SavedConversation
	if resumeID != "" {
		if c, err := h.db.GetConversation(resumeID); err == nil {
			resumeConv = c
			_ = h.db.UpdateConversationResumed(resumeID)
		}
	}

	// Connect to Gemini Live with dynamic fallback chain
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
		errMsg := fmt.Sprintf("Failed to establish Gemini Live session with any candidate model. Last error: %v", lastErr)
		log.Printf("[Hub] %s", errMsg)
		_ = clientWS.WriteJSON(ClientOutMessage{
			Type:    "error",
			Message: errMsg,
		})
		return
	}
	defer geminiWS.Close()

	// Notify browser of successful connection
	_ = clientWS.WriteJSON(ClientOutMessage{
		Type:    "connected",
		Model:   activeModel,
		Persona: targetPersona.Name,
	})

	// If resuming conversation, send initial client content to prompt AI greeting
	if resumeConv != nil {
		resumePrompt := fmt.Sprintf(
			"Hello! I am back. Let's resume our previous discussion about '%s'. Please greet me warmly and pick up from where we left off.",
			resumeConv.TopicTitle,
		)
		initialTurn := gemini.LiveClientMessage{
			ClientContent: &gemini.LiveClientContent{
				Turns: []gemini.LiveContent{
					{
						Role: "user",
						Parts: []gemini.LivePart{
							{Text: resumePrompt},
						},
					},
				},
				TurnComplete: true,
			},
		}
		_ = geminiWS.WriteJSON(initialTurn)
	}

	// Safety ceiling: Maximum 45 minutes per call session to strictly prevent runaway API costs
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Gemini -> Browser
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, rawMsg, err := geminiWS.conn.ReadMessage()
			if err != nil {
				log.Printf("[Hub] Gemini WS read closed: %v", err)
				return
			}

			var serverMsg gemini.LiveServerMessage
			if err := json.Unmarshal(rawMsg, &serverMsg); err != nil {
				log.Printf("[Hub] Failed to parse Gemini server message: %v", err)
				continue
			}

			// Handle Audio & Turn Content
			if serverMsg.ServerContent != nil {
				if serverMsg.ServerContent.Interrupted {
					_ = clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})
				}

				if serverMsg.ServerContent.ModelTurn != nil {
					for _, part := range serverMsg.ServerContent.ModelTurn.Parts {
						if part.InlineData != nil && part.InlineData.Data != "" {
							_ = clientWS.WriteJSON(ClientOutMessage{
								Type: "audio",
								Data: part.InlineData.Data,
							})
						}
					}
				}

				if serverMsg.ServerContent.TurnComplete {
					_ = clientWS.WriteJSON(ClientOutMessage{Type: "turn_complete"})
				}
			}

			// Handle Tool Calls (save_conversation)
			if serverMsg.ToolCall != nil && len(serverMsg.ToolCall.FunctionCalls) > 0 {
				for _, fc := range serverMsg.ToolCall.FunctionCalls {
					if fc.Name == "save_conversation" {
						h.handleSaveConversationTool(geminiWS, clientWS, targetPersona.ID, fc)
					}
				}
			}
		}
	}()

	// Goroutine 2: Browser -> Gemini
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var clientMsg ClientInMessage
			if err := clientWS.conn.ReadJSON(&clientMsg); err != nil {
				log.Printf("[Hub] Client WS read closed: %v", err)
				return
			}

			switch clientMsg.Type {
			case "audio":
				if clientMsg.Data != "" {
					inputMsg := gemini.LiveClientMessage{
						RealtimeInput: &gemini.LiveRealtimeInput{
							MediaChunks: []gemini.LiveBlob{
								{
									MimeType: "audio/pcm;rate=16000",
									Data:     clientMsg.Data,
								},
							},
						},
					}
					if err := geminiWS.WriteJSON(inputMsg); err != nil {
						log.Printf("[Hub] Error writing audio to Gemini: %v", err)
						return
					}
				}

			case "pass_turn":
				// User explicitly pressed "Pass Turn" / "Átadom a szót"
				passMsg := gemini.LiveClientMessage{
					ClientContent: &gemini.LiveClientContent{
						Turns: []gemini.LiveContent{
							{
								Role: "user",
								Parts: []gemini.LivePart{
									{Text: "[The student has finished speaking and handed the turn to you. Please proceed with your response.]"},
								},
							},
						},
						TurnComplete: true,
					},
				}
				_ = geminiWS.WriteJSON(passMsg)

			case "bookmark":
				// User pressed "Bookmark" / "Téma mentése"
				bookmarkMsg := gemini.LiveClientMessage{
					ClientContent: &gemini.LiveClientContent{
						Turns: []gemini.LiveContent{
							{
								Role: "user",
								Parts: []gemini.LivePart{
									{Text: "Please save this conversation topic now using the save_conversation tool, summarize what we discussed, note key vocabulary/points, and the open question."},
								},
							},
						},
						TurnComplete: true,
					},
				}
				_ = geminiWS.WriteJSON(bookmarkMsg)

			case "interrupt", "client_interrupt":
				// Barge-in: user interrupted the AI
				// Notify browser so audio queue is immediately flushed
				_ = clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})
			}
		}
	}()

	wg.Wait()
}

// connectToGeminiLive dials the Gemini Live WebSocket endpoint and performs the Setup handshake
func (h *Hub) connectToGeminiLive(
	model string,
	apiKey string,
	persona *db.Persona,
	profile *db.UserProfile,
	resume *db.SavedConversation,
) (*SafeWebSocket, error) {
	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContent?key=%s",
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

	// Add Resume Context if applicable
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
		// Some implementations may return serverContent or error
		log.Printf("[Hub] First response was not explicitly setupComplete: %s", string(firstRaw))
	} else {
		log.Printf("[Hub] Received setupComplete from Gemini Live for model %s", model)
	}

	return safeWS, nil
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
