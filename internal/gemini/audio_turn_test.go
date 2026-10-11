package gemini_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"english-teacher-app/internal/config"
	"english-teacher-app/internal/gemini"
)

func TestClientContentAudioTurn(t *testing.T) {
	cfg := config.LoadConfig()
	apiKey := cfg.GeminiAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		apiKey,
	)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(liveURL, nil)
	if err != nil {
		t.Skipf("Network unavailable: %v", err)
	}
	defer conn.Close()

	// 1. Setup
	setupMsg := gemini.LiveClientMessage{
		Setup: &gemini.LiveSetup{
			Model: "models/gemini-3.8-live",
			GenerationConfig: &gemini.LiveGenerationConfig{
				ResponseModalities: []string{"AUDIO"},
			},
			SystemInstruction: &gemini.LiveContent{
				Parts: []gemini.LivePart{
					{Text: "You are Sarah, an English teacher. If the student speaks, respond to their speech. If the student says nothing, say 'I heard nothing'."},
				},
			},
		},
	}
	if err := conn.WriteJSON(setupMsg); err != nil {
		t.Fatalf("Write setup failed: %v", err)
	}

	var setupResp gemini.LiveServerMessage
	_ = conn.ReadJSON(&setupResp)
	t.Logf("Setup acknowledged: %+v", setupResp.SetupComplete != nil)

	// 2. Test sending audio via clientContent.turns[0].parts[0].inlineData
	pcm := generateSinePCM16(440.0, 1000) // 1 second of 440Hz tone
	b64Audio := base64.StdEncoding.EncodeToString(pcm)

	turnMsg := gemini.LiveClientMessage{
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

	t.Logf("Sending audio via clientContent turn...")
	if err := conn.WriteJSON(turnMsg); err != nil {
		t.Fatalf("Failed to write turnMsg: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	audioChunks := 0
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for Gemini response")
		default:
		}

		var msg gemini.LiveServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("Read error: %v", err)
		}
		if msg.ServerContent != nil {
			if msg.ServerContent.ModelTurn != nil {
				for _, p := range msg.ServerContent.ModelTurn.Parts {
					if p.InlineData != nil {
						audioChunks++
					}
				}
			}
			if msg.ServerContent.TurnComplete {
				t.Logf("Turn completed! Audio chunks received: %d", audioChunks)
				break
			}
		}
	}

	if audioChunks == 0 {
		t.Errorf("Expected audio response from Gemini, got 0 chunks")
	} else {
		t.Logf("SUCCESS! Gemini accepted inline audio in clientContent and returned %d chunks!", audioChunks)
	}
}
