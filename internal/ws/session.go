package ws

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
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
	activityMu               sync.Mutex
	isAiSpeaking             bool
	isStudentSpeaking        bool
	isProcessingTurn         bool
	hasStartedTurnActivity   bool
	droppingAudio            bool
	lastTurnTime             time.Time
	userAudioChunksInTurn    int
	userAudioBytes           []byte
	rawFallbackBytes         []byte
	preRollChunks            [][]byte
	speechChunksInPhrase     int
	silenceChunksAfterPhrase int
	phrasePeakRMS            float64
	userTurnTranscripts      []string
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
			"4. When the student speaks, follow your 3-phase cycle: (1) gentle error correction, (2) brief explanation or practice drill, (3) conversational response with an open question.\n"+
			"5. The student may conclude their thought with 'Your turn!'. When the turn is handed to you, respond promptly.\n\n"+
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
	s.isStudentSpeaking = false
	s.isProcessingTurn = false
	s.hasStartedTurnActivity = false
	s.droppingAudio = true
	s.userAudioChunksInTurn = 0
	s.userAudioBytes = nil
	s.rawFallbackBytes = nil
	s.preRollChunks = nil
	s.speechChunksInPhrase = 0
	s.silenceChunksAfterPhrase = 0
	s.phrasePeakRMS = 0
	s.userTurnTranscripts = nil
	s.activityMu.Unlock()

	// 1. Tell browser audio has stopped immediately
	_ = s.clientWS.WriteJSON(ClientOutMessage{Type: "interrupted"})
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
			// Real-time server-side voice trigger detection via Gemini input transcription if present
			var transcriptText string
			if serverMsg.ServerContent.InputTranscription != nil && serverMsg.ServerContent.InputTranscription.Text != "" {
				transcriptText = serverMsg.ServerContent.InputTranscription.Text
			} else if serverMsg.ServerContent.InterimInputTranscription != nil && serverMsg.ServerContent.InterimInputTranscription.Text != "" {
				transcriptText = serverMsg.ServerContent.InterimInputTranscription.Text
			}

			if transcriptText != "" {
				log.Printf("[RollingSession] 🗣️ User speech recognized: %q", transcriptText)
				_ = s.clientWS.WriteJSON(ClientOutMessage{
					Type: "user_transcript",
					Text: transcriptText,
				})
				if s.matchesVoiceTrigger(transcriptText) {
					log.Printf("[RollingSession] 🎯 Voice trigger detected in transcript (%q)! Committing turn immediately...", transcriptText)
					s.endUserTurn(ctx, fmt.Sprintf("voice trigger (%q)", transcriptText))
				}
			}

			if serverMsg.ServerContent.OutputTranscription != nil && serverMsg.ServerContent.OutputTranscription.Text != "" {
				currentModelTurnText.WriteString(serverMsg.ServerContent.OutputTranscription.Text)
			}

			if serverMsg.ServerContent.Interrupted {
				s.activityMu.Lock()
				s.isAiSpeaking = false
				s.isProcessingTurn = false
				s.hasStartedTurnActivity = false
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
				s.isStudentSpeaking = false
				s.hasStartedTurnActivity = false
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
				s.hasStartedTurnActivity = false
				s.droppingAudio = false
				s.userAudioChunksInTurn = 0
				s.userAudioBytes = nil
				s.rawFallbackBytes = nil
				s.preRollChunks = nil
				s.speechChunksInPhrase = 0
				s.silenceChunksAfterPhrase = 0
				s.phrasePeakRMS = 0
				s.userTurnTranscripts = nil
				s.lastTurnTime = time.Now()
				s.activityMu.Unlock()

				fullText := strings.TrimSpace(currentModelTurnText.String())
				if fullText != "" {
					s.recordTurn(s.targetPersona.Name, fullText)
				}
				currentModelTurnText.Reset()

				log.Printf("[RollingSession] 🔊 %s turn completed. Forwarding turn_complete to student browser.", s.targetPersona.Name)
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

	// 2. Start Gemini reader for initial connection
	go s.readFromGeminiLoop(ctx, s.activeGemini)

	// 3. Send initial greeting prompt so Sarah speaks first
	s.sendInitialGreeting()

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
				rawChunk, decErr := base64.StdEncoding.DecodeString(clientMsg.Data)
				if decErr == nil && len(rawChunk) > 0 {
					rms := computePCM16RMS(rawChunk)
					var wavToTranscribe []byte

					s.activityMu.Lock()
					canStream := !s.isAiSpeaking && !s.isProcessingTurn
					if canStream {
						// Always maintain a raw 45-second fallback buffer so even ultra-quiet mics are never lost on pass_turn
						s.rawFallbackBytes = append(s.rawFallbackBytes, rawChunk...)
						const maxFallbackBytes = 16000 * 2 * 45
						if len(s.rawFallbackBytes) > maxFallbackBytes {
							s.rawFallbackBytes = s.rawFallbackBytes[len(s.rawFallbackBytes)-maxFallbackBytes:]
						}

						// Adaptive speech vs. pause detection:
						// Works both in quiet rooms and when 3.0x pre-amp elevates ambient noise floor
						isSpeech := rms > 0.008 && (s.phrasePeakRMS < 0.015 || rms > s.phrasePeakRMS*0.30)

						if isSpeech {
							if rms > s.phrasePeakRMS {
								s.phrasePeakRMS = rms
							}
							// Active speech chunk: flush any pre-roll chunks first to preserve consonant onsets
							for _, pr := range s.preRollChunks {
								s.userAudioBytes = append(s.userAudioBytes, pr...)
								s.userAudioChunksInTurn++
							}
							s.preRollChunks = nil

							s.userAudioBytes = append(s.userAudioBytes, rawChunk...)
							s.userAudioChunksInTurn++
							s.speechChunksInPhrase++
							s.silenceChunksAfterPhrase = 0
							s.lastTurnTime = time.Now()
						} else {
							// Silence or ambient background noise after speech
							s.silenceChunksAfterPhrase++
							if s.userAudioChunksInTurn > 0 && s.silenceChunksAfterPhrase <= 15 {
								// Preserve up to 1.5s of natural trailing pause after speech
								s.userAudioBytes = append(s.userAudioBytes, rawChunk...)
								s.userAudioChunksInTurn++
							} else {
								// During long thinking pauses (>1.5s), maintain a 500ms (5-chunk) pre-roll buffer
								cp := make([]byte, len(rawChunk))
								copy(cp, rawChunk)
								s.preRollChunks = append(s.preRollChunks, cp)
								if len(s.preRollChunks) > 5 {
									s.preRollChunks = s.preRollChunks[len(s.preRollChunks)-5:]
								}
							}

							// Fast phrase-end check: 500ms pause after >= 2 speech chunks
							if s.speechChunksInPhrase >= 2 && s.silenceChunksAfterPhrase == 5 {
								const tailMaxBytes = 112000 // ~3.5s at 16kHz 16-bit mono
								startIdx := 0
								if len(s.userAudioBytes) > tailMaxBytes {
									startIdx = len(s.userAudioBytes) - tailMaxBytes
								}
								if startIdx%2 != 0 {
									startIdx++
								}
								tailPCM := make([]byte, len(s.userAudioBytes)-startIdx)
								copy(tailPCM, s.userAudioBytes[startIdx:])
								normTail := normalizePCM16(tailPCM)
								wavToTranscribe = encodePCM16ToWAV(normTail, 16000)
								s.speechChunksInPhrase = 0
								s.phrasePeakRMS = 0
							}
						}

						// Safety ceiling on single turn buffer (max 3 minutes: ~5.76MB)
						const maxTurnBytes = 16000 * 2 * 180
						if len(s.userAudioBytes) > maxTurnBytes {
							s.userAudioBytes = s.userAudioBytes[len(s.userAudioBytes)-maxTurnBytes:]
						}
					}
					s.activityMu.Unlock()

					if len(wavToTranscribe) > 0 {
						go s.checkVoiceTriggerInTail(ctx, wavToTranscribe)
					}
				}
			}

		case "pass_turn":
			s.endUserTurn(ctx, "client pass_turn")

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

func (s *RollingLiveSession) matchesVoiceTrigger(text string) bool {
	clean := strings.ToLower(text)
	var b strings.Builder
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, " ")

	// Unconditional turn-handover triggers (almost never used mid-sentence)
	unconditional := []string{
		"your turn",
		"you re turn",
		"you are turn",
		"ur turn",
		"over to you",
		"go ahead",
	}
	for _, u := range unconditional {
		if strings.Contains(joined, u) {
			return true
		}
	}

	// Question handovers: only if they appear at or near the end of what was spoken (last 6 words)
	recentWords := words
	if len(words) > 6 {
		recentWords = words[len(words)-6:]
	}
	recentJoined := strings.Join(recentWords, " ")
	questionTriggers := []string{
		"what do you think",
		"what are your thoughts",
	}
	for _, q := range questionTriggers {
		if strings.Contains(recentJoined, q) {
			return true
		}
	}

	return false
}

func (s *RollingLiveSession) checkVoiceTriggerInTail(ctx context.Context, wavBytes []byte) {
	s.activityMu.Lock()
	busy := s.isProcessingTurn || s.isAiSpeaking
	s.activityMu.Unlock()
	if busy {
		return
	}

	transcript, err := s.hub.TranscribeAudioClip(ctx, s.apiKey, wavBytes)
	if err != nil || transcript == "" {
		return
	}

	s.activityMu.Lock()
	stillListening := !s.isProcessingTurn && !s.isAiSpeaking
	if stillListening {
		s.userTurnTranscripts = append(s.userTurnTranscripts, transcript)
	}
	s.activityMu.Unlock()

	if !stillListening {
		return
	}

	log.Printf("[RollingSession] 🗣️ Phrase transcribed: %q", transcript)
	_ = s.clientWS.WriteJSON(ClientOutMessage{
		Type: "user_transcript",
		Text: transcript,
	})

	if s.matchesVoiceTrigger(transcript) {
		log.Printf("[RollingSession] 🎯 Voice trigger detected in phrase (%q)! Ending user turn immediately...", transcript)
		s.endUserTurn(ctx, fmt.Sprintf("voice trigger (%q)", transcript))
	}
}

func (s *RollingLiveSession) endUserTurn(ctx context.Context, source string) {
	s.activityMu.Lock()
	if s.isProcessingTurn || s.isAiSpeaking {
		s.activityMu.Unlock()
		return
	}
	s.isProcessingTurn = true
	s.droppingAudio = false

	// Fallback to rawFallbackBytes if speech threshold was not reached before manual pass_turn
	if len(s.userAudioBytes) == 0 && len(s.rawFallbackBytes) > 0 {
		s.userAudioBytes = s.rawFallbackBytes
		s.userAudioChunksInTurn = len(s.rawFallbackBytes) / 3200
		if s.userAudioChunksInTurn == 0 {
			s.userAudioChunksInTurn = 1
		}
	}

	chunksCount := s.userAudioChunksInTurn
	rawPCM := s.userAudioBytes
	transcripts := strings.Join(s.userTurnTranscripts, " ")

	s.userAudioBytes = nil
	s.rawFallbackBytes = nil
	s.preRollChunks = nil
	s.userAudioChunksInTurn = 0
	s.speechChunksInPhrase = 0
	s.silenceChunksAfterPhrase = 0
	s.phrasePeakRMS = 0
	s.userTurnTranscripts = nil
	s.hasStartedTurnActivity = false
	turnStartTime := time.Now()
	s.lastTurnTime = turnStartTime
	s.activityMu.Unlock()

	if transcripts != "" {
		s.recordTurn(s.profile.DisplayName, transcripts)
	}

	var endMsg gemini.LiveClientMessage
	if chunksCount > 0 && len(rawPCM) > 0 {
		normPCM := normalizePCM16(rawPCM)
		b64Audio := base64.StdEncoding.EncodeToString(normPCM)
		durationSec := float64(len(normPCM)) / 32000.0
		log.Printf("[RollingSession] 🎯 %s -> Sending %.1fs normalized audio (%d chunks, %d bytes) to Gemini via ClientContent...",
			source, durationSec, chunksCount, len(normPCM))

		endMsg = gemini.LiveClientMessage{
			ClientContent: &gemini.LiveClientContent{
				Turns: []gemini.LiveContent{
					{
						Role: "user",
						Parts: []gemini.LivePart{
							{
								InlineData: &gemini.LiveBlob{
									MimeType: "audio/pcm;rate=16000",
									Data:     b64Audio,
								},
							},
						},
					},
				},
				TurnComplete: true,
			},
		}
		if err := s.sendToActiveGemini(endMsg); err != nil {
			log.Printf("[RollingSession] Error sending audio turn to Gemini: %v. Attempting immediate reconnect...", err)
			if rErr := s.doRollover(ctx); rErr == nil {
				_ = s.sendToActiveGemini(endMsg)
			}
		}
	} else {
		log.Printf("[RollingSession] ℹ️ Zero audio chunks on %s. Sending gentle prompt to Gemini...", source)
		endMsg = gemini.LiveClientMessage{
			ClientContent: &gemini.LiveClientContent{
				Turns: []gemini.LiveContent{
					{
						Role: "user",
						Parts: []gemini.LivePart{
							{Text: "[The student passed the turn to you without speaking. Please encourage them gently, and ask an open question to invite them to speak.]"},
						},
					},
				},
				TurnComplete: true,
			},
		}
		_ = s.sendToActiveGemini(endMsg)
	}

	// Always notify client that turn was passed so UI updates and chime plays
	_ = s.clientWS.WriteJSON(ClientOutMessage{
		Type: "turn_passed",
	})

	// Turn watchdog: If Gemini does not respond after 12s, refresh connection cleanly
	go func(sentTime time.Time, retryMsg gemini.LiveClientMessage) {
		time.Sleep(12 * time.Second)
		s.activityMu.Lock()
		stalled := s.isProcessingTurn && !s.isAiSpeaking && s.lastTurnTime.Equal(sentTime)
		s.activityMu.Unlock()

		if stalled {
			log.Printf("[RollingSession] ⚠️ Turn response watchdog triggered (no response from Gemini in 12s on %s). Refreshing connection...", source)
			if rErr := s.doRollover(ctx); rErr == nil {
				_ = s.sendToActiveGemini(retryMsg)
			}
		}
	}(turnStartTime, endMsg)
}

// computePCM16RMS computes root-mean-square amplitude of 16-bit little-endian PCM
func computePCM16RMS(pcm []byte) float64 {
	numSamples := len(pcm) / 2
	if numSamples == 0 {
		return 0
	}
	var sumSquares float64
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(pcm[i*2]) | (uint16(pcm[i*2+1]) << 8))
		norm := float64(sample) / 32768.0
		sumSquares += norm * norm
	}
	return math.Sqrt(sumSquares / float64(numSamples))
}

// normalizePCM16 normalizes 16-bit little-endian PCM to peak amplitude 22000 (~67% full scale)
// with maximum gain 12.0x, ensuring quiet microphones are crystal-clear without distortion.
func normalizePCM16(pcm []byte) []byte {
	numSamples := len(pcm) / 2
	if numSamples == 0 {
		return pcm
	}

	var maxAbs int32
	for i := 0; i < numSamples; i++ {
		sample := int32(int16(uint16(pcm[i*2]) | (uint16(pcm[i*2+1]) << 8)))
		if sample < 0 {
			sample = -sample
		}
		if sample > maxAbs {
			maxAbs = sample
		}
	}

	const targetPeak = 22000.0
	if maxAbs < 120 || maxAbs >= 20000 {
		out := make([]byte, len(pcm))
		copy(out, pcm)
		return out
	}

	gain := targetPeak / float64(maxAbs)
	if gain > 12.0 {
		gain = 12.0
	}

	out := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(pcm[i*2]) | (uint16(pcm[i*2+1]) << 8))
		scaled := int32(float64(sample) * gain)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		u := uint16(int16(scaled))
		out[i*2] = byte(u & 0xFF)
		out[i*2+1] = byte((u >> 8) & 0xFF)
	}
	return out
}

// encodePCM16ToWAV wraps raw 16-bit mono PCM into a standard 44-byte RIFF WAV format
func encodePCM16ToWAV(pcm []byte, sampleRate int) []byte {
	dataLen := uint32(len(pcm))
	totalLen := 44 + dataLen
	wav := make([]byte, totalLen)

	// RIFF chunk
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], 36+dataLen)
	copy(wav[8:12], "WAVE")

	// fmt subchunk
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)

	// data subchunk
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], dataLen)
	copy(wav[44:], pcm)

	return wav
}
