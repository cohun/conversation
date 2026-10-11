package gemini_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProbeBidiModels(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	versions := []string{"v1alpha", "v1beta"}
	models := []string{
		"models/gemini-2.0-flash-exp",
		"models/gemini-2.0-flash-realtime-exp",
		"models/gemini-2.0-flash",
		"models/gemini-2.5-flash",
		"models/gemini-flash-latest",
		"models/gemini-3.8-flash",
		"models/gemini-3.8-live",
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	for _, v := range versions {
		for _, m := range models {
			url := fmt.Sprintf("wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.%s.GenerativeService.BidiGenerateContent?key=%s", v, key)
			conn, _, err := dialer.Dial(url, nil)
			if err != nil {
				t.Logf("[%s][%s] Dial error: %v", v, m, err)
				continue
			}

			setup := map[string]interface{}{
				"setup": map[string]interface{}{
					"model": m,
					"generationConfig": map[string]interface{}{
						"responseModalities": []string{"AUDIO"},
					},
				},
			}
			data, _ := json.Marshal(setup)
			_ = conn.WriteMessage(websocket.TextMessage, data)

			_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
			_, respData, rErr := conn.ReadMessage()
			conn.Close()

			if rErr != nil {
				if strings.Contains(rErr.Error(), "1008") {
					t.Logf("[%s][%s] ❌ 1008: not supported/found", v, m)
				} else {
					t.Logf("[%s][%s] ❌ Read error: %v", v, m, rErr)
				}
			} else {
				t.Logf("🎯 SUCCESS! [%s][%s] acknowledged setup: %s", v, m, string(respData)[:min(len(respData), 80)])
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
