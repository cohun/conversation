package gemini_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"english-teacher-app/internal/config"
	"english-teacher-app/internal/gemini"
)

// generateSinePCM16 creates 100ms of 16kHz PCM audio
func generateSinePCM16(freqHz float64, durationMs int) []byte {
	numSamples := 16000 * durationMs / 1000
	buf := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / 16000.0
		sample := int16(math.Sin(2*math.Pi*freqHz*t) * 12000)
		buf[i*2] = byte(sample & 0xFF)
		buf[i*2+1] = byte((sample >> 8) & 0xFF)
	}
	return buf
}

func TestLiveAudioInputResponse(t *testing.T) {
	cfg := config.LoadConfig()
	apiKey := cfg.GeminiAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("Skipping live test: GEMINI_API_KEY not set")
	}

	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		apiKey,
	)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(liveURL, nil)
	if err != nil {
		t.Skipf("Skipping live test (network unavailable): %v", err)
	}
	defer conn.Close()

	// 1. Setup
	setupMsg := gemini.LiveClientMessage{
		Setup: &gemini.LiveSetup{
			Model: "models/gemini-3.8-live",
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
			SystemInstruction: &gemini.LiveContent{
				Parts: []gemini.LivePart{
					{Text: "You are Sarah, an encouraging English teacher. Listen to the student's audio input. Greet them by name Attila."},
				},
			},
		},
	}
	if err := conn.WriteJSON(setupMsg); err != nil {
		t.Fatalf("Failed to write setup: %v", err)
	}

	// Read setup complete
	var resp gemini.LiveServerMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("Failed to read setup resp: %v", err)
	}
	t.Logf("1. Setup acknowledged: %+v", resp.SetupComplete != nil)

	// 2. Initial greeting
	greetingMsg := gemini.LiveClientMessage{
		ClientContent: &gemini.LiveClientContent{
			Turns: []gemini.LiveContent{
				{
					Role: "user",
					Parts: []gemini.LivePart{
						{Text: "Hello Sarah! Greet me warmly by name Attila."},
					},
				},
			},
			TurnComplete: true,
		},
	}
	_ = conn.WriteJSON(greetingMsg)

	// Consume Sarah's greeting until TurnComplete
	for {
		var msg gemini.LiveServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("Read error greeting: %v", err)
		}
		if msg.ServerContent != nil && msg.ServerContent.TurnComplete {
			t.Logf("2. Initial greeting completed.")
			break
		}
	}

	// 3. Now simulate student streaming 20 chunks (2 seconds) of 16kHz PCM audio
	t.Logf("3. Streaming 20 chunks of real-time audio (440Hz tone simulating voice)...")
	for i := 0; i < 20; i++ {
		pcmChunk := generateSinePCM16(440.0, 100)
		b64 := base64.StdEncoding.EncodeToString(pcmChunk)
		audioMsg := gemini.LiveClientMessage{
			RealtimeInput: &gemini.LiveRealtimeInput{
				MediaChunks: []gemini.LiveBlob{
					{
						MimeType: "audio/pcm;rate=16000",
						Data:     b64,
					},
				},
			},
		}
		if err := conn.WriteJSON(audioMsg); err != nil {
			t.Fatalf("Failed to write audio chunk %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 4. Student clicks 'Átadom a szót'
	t.Logf("4. Sending pass_turn (TurnComplete: true)...")
	passMsg := gemini.LiveClientMessage{
		ClientContent: &gemini.LiveClientContent{
			TurnComplete: true,
		},
	}
	if err := conn.WriteJSON(passMsg); err != nil {
		t.Fatalf("Failed to write passMsg: %v", err)
	}

	// 5. Read response from Gemini
	startWait := time.Now()
	audioChunksReceived := 0
	var textParts []string

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	readDone := make(chan bool)
	go func() {
		for {
			var msg gemini.LiveServerMessage
			if err := conn.ReadJSON(&msg); err != nil {
				t.Logf("Read error in response: %v", err)
				break
			}
			raw, _ := json.Marshal(msg)
			if strings.Contains(string(raw), "text") {
				t.Logf("Received message with text: %s", string(raw))
			}
			if msg.ServerContent != nil {
				if msg.ServerContent.ModelTurn != nil {
					for _, p := range msg.ServerContent.ModelTurn.Parts {
						if p.InlineData != nil {
							audioChunksReceived++
						}
						if p.Text != "" {
							textParts = append(textParts, p.Text)
						}
					}
				}
				if msg.ServerContent.TurnComplete {
					t.Logf("5. TurnComplete received from Gemini! Total audio chunks: %d, latency: %v",
						audioChunksReceived, time.Since(startWait))
					readDone <- true
					return
				}
			}
		}
	}()

	select {
	case <-readDone:
		t.Logf("SUCCESS! Sarah responded with %d audio chunks, text: %v", audioChunksReceived, textParts)
	case <-ctx.Done():
		t.Fatalf("TIMEOUT waiting for response from Gemini")
	}
}
