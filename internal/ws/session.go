package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"english-teacher-app/internal/db"
	"english-teacher-app/internal/gemini"
)

type TurnRecord struct {
	Speaker string
	Text    string
}

// RollingLiveSession manages an ongoing voice call with seamless background rollovers
// and automatic emergency reconnects to eliminate Gemini Live 10-minute timeouts and token context exhaustion.
type RollingLiveSession struct {
	hub           *Hub
	clientWS      *SafeWebSocket
	targetPersona *db.Persona
	profile       *db.UserProfile
	resumeConv    *db.SavedConversation
	apiKey        string
	model         string

	mu           sync.RWMutex
	activeGemini *SafeWebSocket
	isClosing    bool

	// History buffer to preserve conversational context across rollovers
	historyMu sync.Mutex
	turns     []TurnRecord

	// Activity tracking to perform rollovers during natural conversational pauses
	activityMu       sync.Mutex
	isAiSpeaking     bool
	isProcessingTurn bool
	droppingAudio    bool
	lastTurnTime     time.Time
}

func newRollingLiveSession(
	hub *Hub,
	clientWS *SafeWebSocket,
	persona *db.Persona,
	profile *db.UserProfile,
	resume *db.SavedConversation,
	apiKey string,
	model string,
	initialWS *SafeWebSocket,
) *RollingLiveSession {
	return &RollingLiveSession{
		hub:           hub,
		clientWS:      clientWS,
		targetPersona: persona,
		profile:       profile,
		resumeConv:    resume,
		apiKey:        apiKey,
		model:         model,
		activeGemini:  initialWS,
		lastTurnTime:  time.Now(),
	}
}

func (s *RollingLiveSession) getActiveGemini() *SafeWebSocket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeGemini
}

func (s *RollingLiveSession) sendToActiveGemini(msg interface{}) error {
	s.mu.RLock()
	ws := s.activeGemini
	closing := s.isClosing
	s.mu.RUnlock()

	if closing || ws == nil {
		return fmt.Errorf("session closing or not connected")
	}
	return ws.WriteJSON(msg)
}

func (s *RollingLiveSession) recordTurn(speaker, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()

	s.turns = append(s.turns, TurnRecord{
		Speaker: speaker,
		Text:    text,
	})
	// Keep up to the last 12 complete turns in history
	if len(s.turns) > 12 {
		s.turns = s.turns[len(s.turns)-12:]
	}
}

func (s *RollingLiveSession) buildContinuationPrompt() string {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf(
		"[Ongoing Phone Call Continuation]\n"+
			"You are %s, an encouraging English teacher, currently on a live telephone call with your student %s.\n"+
			"This is a seamless continuation of your ongoing call.\n"+
			"RULES:\n"+
			"1. Do NOT greet the student again, do NOT re-introduce yourself, and do NOT restart the lesson.\n"+
			"2. Maintain your warm, natural conversational teacher tone.\n"+
			"3. Continue directly from the ongoing conversation below.\n"+
			"4. When the student speaks, follow your 3-phase cycle: (1) gentle error correction, (2) brief explanation or practice drill, (3) conversational response with an open question.\n\n"+
			"Recent Conversation Transcript:\n",
		s.targetPersona.Name,
		s.profile.DisplayName,
	))

	if len(s.turns) == 0 {
		buf.WriteString("- [Call in progress]\n")
	} else {
		start := 0
		if len(s.turns) > 8 {
			start = len(s.turns) - 8
		}
		for _, t := range s.turns[start:] {
			buf.WriteString(fmt.Sprintf("- %s: %s\n", t.Speaker, t.Text))
		}
	}

	buf.WriteString("\nInstruction: Wait for the student to speak, then respond naturally following the context above.")
	return buf.String()
}

func (s *RollingLiveSession) doRollover(ctx context.Context) error {
	s.mu.RLock()
	closing := s.isClosing
	s.mu.RUnlock()
	if closing {
		return nil
	}

	continuationContext := s.buildContinuationPrompt()
	newWS, err := s.hub.connectToGeminiLiveWithContinuation(
		s.model,
		s.apiKey,
		s.targetPersona,
		s.profile,
		s.resumeConv,
		continuationContext,
	)
	if err != nil {
		return err
	}

	s.mu.Lock()
	oldWS := s.activeGemini
	s.activeGemini = newWS
	s.mu.Unlock()

	// Start reading from new connection
	go s.readFromGeminiLoop(ctx, newWS)

	// Clean up old connection safely after 2s to let any pending chunks flush
	go func(wsToClose *SafeWebSocket) {
		time.Sleep(2000 * time.Millisecond)
		if wsToClose != nil {
			_ = wsToClose.Close()
		}
	}(oldWS)

	log.Printf("[RollingSession] ✅ Seamless session rollover completed! Context refreshed, call continuous.")
	return nil
}

func (s *RollingLiveSession) rolloverTickerLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Wait for a natural pause in conversation (AI not speaking AND no turn in flight)
			// Wait up to 60 seconds (120 * 500ms)
			for i := 0; i < 120; i++ {
				s.activityMu.Lock()
				busy := s.isAiSpeaking || s.isProcessingTurn
				s.activityMu.Unlock()
				if !busy {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}

			s.mu.RLock()
			closing := s.isClosing
			s.mu.RUnlock()
			if closing {
				return
			}

			log.Printf("[RollingSession] 🔄 Triggering proactive rollover (refreshing session & token context) for %s...", s.targetPersona.Name)
			if err := s.doRollover(ctx); err != nil {
				log.Printf("[RollingSession] ⚠️ Proactive rollover failed (%v), existing session remains active", err)
			}
		}
	}
}

func (s *RollingLiveSession) handleClientInterrupt() {
	s.activityMu.Lock()
	s.isAiSpeaking = false
	s.isProcessingTurn = false
	s.droppingAudio = true
	s.activityMu.Unlock()

	// 1. Tell browser audio has stopped immediately
	_ = s.clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})

	// 2. Send barge-in activity signals to Gemini Live to stop ongoing generation
	interruptMsg := gemini.LiveClientMessage{
		RealtimeInput: &gemini.LiveRealtimeInput{
			ActivityStart: &gemini.LiveActivityStart{},
		},
	}
	_ = s.sendToActiveGemini(interruptMsg)

	endMsg := gemini.LiveClientMessage{
		RealtimeInput: &gemini.LiveRealtimeInput{
			ActivityEnd: &gemini.LiveActivityEnd{},
		},
	}
	_ = s.sendToActiveGemini(endMsg)
}

func (s *RollingLiveSession) readFromGeminiLoop(ctx context.Context, ws *SafeWebSocket) {
	var currentModelTurnText strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, rawMsg, err := ws.conn.ReadMessage()
		if err != nil {
			s.mu.RLock()
			isActive := (s.activeGemini == ws)
			closing := s.isClosing
			s.mu.RUnlock()

			// If this connection was retired by a rollover or call is ending, exit cleanly
			if !isActive || closing {
				return
			}

			// Unexpected drop of the active connection: auto-reconnect immediately!
			log.Printf("[RollingSession] ⚠️ Active Gemini connection dropped unexpectedly: %v. Initiating emergency reconnect...", err)
			if reconnectErr := s.doRollover(ctx); reconnectErr != nil {
				log.Printf("[RollingSession] ❌ Emergency reconnect failed: %v", reconnectErr)
				_ = s.clientWS.WriteJSON(ClientOutMessage{
					Type:    "error",
					Message: "Gemini connection was interrupted. Please tap 'Resume Call' to continue.",
				})
				_ = s.clientWS.Close()
			}
			return
		}

		var serverMsg gemini.LiveServerMessage
		if err := json.Unmarshal(rawMsg, &serverMsg); err != nil {
			log.Printf("[RollingSession] Failed to parse Gemini server message: %v", err)
			continue
		}

		// Handle Audio & Turn Content
		if serverMsg.ServerContent != nil {
			if serverMsg.ServerContent.Interrupted {
				s.activityMu.Lock()
				s.isAiSpeaking = false
				s.isProcessingTurn = false
				s.activityMu.Unlock()

				interruptedText := strings.TrimSpace(currentModelTurnText.String())
				if interruptedText != "" {
					s.recordTurn(s.targetPersona.Name+" (interrupted)", interruptedText)
				}
				currentModelTurnText.Reset()

				_ = s.clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})
			}

			if serverMsg.ServerContent.ModelTurn != nil {
				s.activityMu.Lock()
				s.isAiSpeaking = true
				s.activityMu.Unlock()

				for _, part := range serverMsg.ServerContent.ModelTurn.Parts {
					if part.InlineData != nil && part.InlineData.Data != "" {
						s.activityMu.Lock()
						dropping := s.droppingAudio
						s.activityMu.Unlock()

						if !dropping {
							_ = s.clientWS.WriteJSON(ClientOutMessage{
								Type: "audio",
								Data: part.InlineData.Data,
							})
						}
					}
					if part.Text != "" {
						currentModelTurnText.WriteString(part.Text)
					}
				}
			}

			if serverMsg.ServerContent.TurnComplete {
				s.activityMu.Lock()
				s.isAiSpeaking = false
				s.isProcessingTurn = false
				s.droppingAudio = false
				s.lastTurnTime = time.Now()
				s.activityMu.Unlock()

				fullText := strings.TrimSpace(currentModelTurnText.String())
				if fullText != "" {
					s.recordTurn(s.targetPersona.Name, fullText)
				}
				currentModelTurnText.Reset()

				_ = s.clientWS.WriteJSON(ClientOutMessage{Type: "turn_complete"})
			}
		}

		// Handle Tool Calls (save_conversation)
		if serverMsg.ToolCall != nil && len(serverMsg.ToolCall.FunctionCalls) > 0 {
			for _, fc := range serverMsg.ToolCall.FunctionCalls {
				if fc.Name == "save_conversation" {
					s.hub.handleSaveConversationTool(ws, s.clientWS, s.targetPersona.ID, fc)
				}
			}
		}
	}
}

func (s *RollingLiveSession) sendInitialGreeting() {
	var greetingPrompt string
	if s.resumeConv != nil {
		greetingPrompt = fmt.Sprintf(
			"Hello! I am back. Let's resume our previous discussion about '%s'. Please greet me warmly by my name (%s) and pick up from where we left off.",
			s.resumeConv.TopicTitle,
			s.profile.DisplayName,
		)
	} else {
		greetingPrompt = fmt.Sprintf(
			"Hello %s! The phone call has just connected. Please greet me warmly by my name (%s), introduce yourself briefly, and invite me to begin our spoken conversation.",
			s.targetPersona.Name,
			s.profile.DisplayName,
		)
	}
	initialTurn := gemini.LiveClientMessage{
		ClientContent: &gemini.LiveClientContent{
			Turns: []gemini.LiveContent{
				{
					Role: "user",
					Parts: []gemini.LivePart{
						{Text: greetingPrompt},
					},
				},
			},
			TurnComplete: true,
		},
	}
	_ = s.sendToActiveGemini(initialTurn)
}

func (s *RollingLiveSession) Run() {
	defer func() {
		s.mu.Lock()
		s.isClosing = true
		ws := s.activeGemini
		s.activeGemini = nil
		s.mu.Unlock()

		if ws != nil {
			_ = ws.Close()
		}
	}()

	// Safety ceiling: Maximum 60 minutes per call session
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	// 1. Notify browser of successful connection
	_ = s.clientWS.WriteJSON(ClientOutMessage{
		Type:    "connected",
		Model:   s.model,
		Persona: s.targetPersona.Name,
	})

	// 2. Send initial greeting prompt so Sarah speaks first
	s.sendInitialGreeting()

	// 3. Start Gemini reader for initial connection
	go s.readFromGeminiLoop(ctx, s.activeGemini)

	// 4. Start Proactive Rollover Loop (every 5.5 minutes) to stay completely clear of timeouts & token exhaustion
	go s.rolloverTickerLoop(ctx, 330*time.Second)

	// 4b. Inactivity Watchdog: cleanly disconnect after 3 minutes (180s) of silence
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.activityMu.Lock()
				idleDuration := time.Since(s.lastTurnTime)
				speaking := s.isAiSpeaking
				busy := s.isProcessingTurn
				s.activityMu.Unlock()

				if idleDuration > 180*time.Second && !speaking && !busy {
					log.Printf("[RollingSession] ⏰ 3 minutes of inactivity detected. Disconnecting call to preserve resources.")
					_ = s.clientWS.WriteJSON(ClientOutMessage{
						Type:    "error",
						Message: "Call ended automatically due to 3 minutes of inactivity.",
					})
					_ = s.clientWS.Close()
					return
				}
			}
		}
	}()

	// 5. Client -> Gemini loop (blocks until client disconnects)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var clientMsg ClientInMessage
		if err := s.clientWS.conn.ReadJSON(&clientMsg); err != nil {
			log.Printf("[RollingSession] Client browser disconnected: %v", err)
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
				_ = s.sendToActiveGemini(inputMsg)
			}

		case "pass_turn":
			s.activityMu.Lock()
			s.isProcessingTurn = true
			s.droppingAudio = false
			turnStartTime := time.Now()
			s.lastTurnTime = turnStartTime
			s.activityMu.Unlock()

			rawUserText := strings.TrimSpace(clientMsg.Text)
			var userTextForGemini string
			if rawUserText == "" {
				userTextForGemini = "[The student has finished speaking their turn. If you heard what they said via audio, respond directly following your 3-phase teaching cycle. If their audio was silent or unclear, gently check in by asking: 'I didn't quite catch that, could you repeat that for me?']"
				s.recordTurn("Student ("+s.profile.DisplayName+")", "[Spoken turn]")
			} else {
				userTextForGemini = rawUserText
				s.recordTurn("Student ("+s.profile.DisplayName+")", rawUserText)
			}

			passMsg := gemini.LiveClientMessage{
				ClientContent: &gemini.LiveClientContent{
					Turns: []gemini.LiveContent{
						{
							Role: "user",
							Parts: []gemini.LivePart{
								{Text: userTextForGemini},
							},
						},
					},
					TurnComplete: true,
				},
			}
			if err := s.sendToActiveGemini(passMsg); err != nil {
				log.Printf("[RollingSession] Error writing pass_turn to Gemini: %v. Attempting immediate reconnect...", err)
				if rErr := s.doRollover(ctx); rErr == nil {
					_ = s.sendToActiveGemini(passMsg)
				}
			} else {
				// Turn Watchdog: If Gemini gives zero response after 12s, auto-recover seamlessly!
				go func(sentTime time.Time, retryMsg gemini.LiveClientMessage) {
					time.Sleep(12 * time.Second)
					s.activityMu.Lock()
					stalled := s.isProcessingTurn && !s.isAiSpeaking && s.lastTurnTime.Equal(sentTime)
					s.activityMu.Unlock()

					if stalled {
						log.Printf("[RollingSession] ⚠️ Turn response watchdog triggered (no response from Gemini in 12s). Seamlessly refreshing connection and retrying turn...")
						if rErr := s.doRollover(ctx); rErr == nil {
							_ = s.sendToActiveGemini(retryMsg)
						}
					}
				}(turnStartTime, passMsg)
			}

		case "bookmark":
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
			_ = s.sendToActiveGemini(bookmarkMsg)

		case "interrupt", "client_interrupt":
			s.handleClientInterrupt()
		}
	}
}
