package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gorilla/websocket"
)

// generateSinePCM creates durationMs of 16kHz 16-bit PCM tone
func generateSinePCM(freqHz float64, durationMs int) []byte {
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

// TestLiveVoiceConnection runs an end-to-end simulated audio call with Gemini Live
func TestLiveVoiceConnection(ctx context.Context, apiKey, model string) (map[string]interface{}, error) {
	if apiKey == "" {
		return nil, errors.New("Gemini API key is not configured")
	}
	if model == "" || model == "auto" {
		model = "models/gemini-3.8-live"
	}

	liveURL := fmt.Sprintf(
		"wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		apiKey,
	)

	dialer := websocket.Dialer{HandshakeTimeout: 7 * time.Second}
	conn, _, err := dialer.DialContext(ctx, liveURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	defer conn.Close()

	// 1. Setup
	setupMsg := LiveClientMessage{
		Setup: &LiveSetup{
			Model: model,
			GenerationConfig: &LiveGenerationConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &LiveSpeechConfig{
					VoiceConfig: &LiveVoiceConfig{
						PrebuiltVoiceConfig: &LivePrebuiltVoiceConfig{
							VoiceName: "Aoede",
						},
					},
				},
			},
			InputAudioTranscription:  &LiveAudioTranscriptionConfig{},
			OutputAudioTranscription: &LiveAudioTranscriptionConfig{},
			SystemInstruction: &LiveContent{
				Parts: []LivePart{
					{Text: "You are Sarah, an encouraging English teacher. Briefly respond to test audio input."},
				},
			},
		},
	}
	if err := conn.WriteJSON(setupMsg); err != nil {
		return nil, fmt.Errorf("write setup failed: %w", err)
	}

	// Read setup acknowledgment
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var setupResp LiveServerMessage
	if err := conn.ReadJSON(&setupResp); err != nil {
		return nil, fmt.Errorf("read setup response failed: %w", err)
	}

	// 2. Initial Greeting
	greetingMsg := LiveClientMessage{
		ClientContent: &LiveClientContent{
			Turns: []LiveContent{
				{
					Role: "user",
					Parts: []LivePart{
						{Text: "Hello Sarah, this is an automated health check test."},
					},
				},
			},
			TurnComplete: true,
		},
	}
	if err := conn.WriteJSON(greetingMsg); err != nil {
		return nil, fmt.Errorf("write greeting failed: %w", err)
	}

	greetingChunks := 0
	var greetingTranscript string
	greetingStart := time.Now()
	for {
		if time.Since(greetingStart) > 10*time.Second {
			return nil, errors.New("timeout waiting for initial greeting")
		}
		_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		var msg LiveServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("reading greeting failed: %w", err)
		}
		if msg.ServerContent != nil {
			if msg.ServerContent.OutputTranscription != nil && msg.ServerContent.OutputTranscription.Text != "" {
				greetingTranscript += msg.ServerContent.OutputTranscription.Text
			}
			if msg.ServerContent.ModelTurn != nil {
				for _, p := range msg.ServerContent.ModelTurn.Parts {
					if p.InlineData != nil {
						greetingChunks++
					}
				}
			}
			if msg.ServerContent.TurnComplete {
				break
			}
		}
	}

	// 3. Stream 10 chunks (1 second) of 16kHz PCM audio via RealtimeInput.MediaChunks burst
	for i := 0; i < 10; i++ {
		pcm := generateSinePCM(440.0, 100)
		b64 := base64.StdEncoding.EncodeToString(pcm)
		audioMsg := LiveClientMessage{
			RealtimeInput: &LiveRealtimeInput{
				MediaChunks: []LiveBlob{
					{
						MimeType: "audio/pcm;rate=16000",
						Data:     b64,
					},
				},
			},
		}
		if err := conn.WriteJSON(audioMsg); err != nil {
			return nil, fmt.Errorf("writing audio chunk %d failed: %w", i, err)
		}
	}

	// 4. Commit buffered audio turn via ClientContent{TurnComplete: true}
	passMsg := LiveClientMessage{
		ClientContent: &LiveClientContent{
			TurnComplete: true,
		},
	}
	turnStartTime := time.Now()
	if err := conn.WriteJSON(passMsg); err != nil {
		return nil, fmt.Errorf("write pass_turn failed: %w", err)
	}

	// 5. Receive response audio chunks
	responseChunks := 0
	var responseTranscript string
	for {
		if time.Since(turnStartTime) > 12*time.Second {
			return nil, errors.New("timeout waiting for audio response from Gemini")
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg LiveServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("reading response audio failed: %w", err)
		}
		if msg.ServerContent != nil {
			if msg.ServerContent.OutputTranscription != nil && msg.ServerContent.OutputTranscription.Text != "" {
				responseTranscript += msg.ServerContent.OutputTranscription.Text
			}
			if msg.ServerContent.ModelTurn != nil {
				for _, p := range msg.ServerContent.ModelTurn.Parts {
					if p.InlineData != nil {
						responseChunks++
					}
				}
			}
			if msg.ServerContent.TurnComplete {
				break
			}
		}
	}

	return map[string]interface{}{
		"status":                "ok",
		"model":                 model,
		"setup_acknowledged":    true,
		"greeting_audio_chunks": greetingChunks,
		"greeting_transcript":   greetingTranscript,
		"user_audio_chunks":     10,
		"response_audio_chunks": responseChunks,
		"response_transcript":   responseTranscript,
		"roundtrip_latency_ms":  time.Since(turnStartTime).Milliseconds(),
		"timestamp":             time.Now().Format(time.RFC3339),
		"message":               "✅ Gemini Live audio test passed successfully!",
	}, nil
}
