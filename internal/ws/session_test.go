package ws

import (
	"testing"
)

func TestMatchesVoiceTrigger(t *testing.T) {
	session := &RollingLiveSession{}

	tests := []struct {
		input    string
		expected bool
	}{
		{"Your turn!", true},
		{"your turn", true},
		{"That is all for now, your turn!", true},
		{"Over to you!", true},
		{"Over to you, Sarah.", true},
		{"Go ahead.", true},
		{"Ur turn.", true},
		{"You're turn!", true},
		{"I like to read books on weekends. What do you think?", true},
		{"What are your thoughts on this?", true},
		// Should NOT trigger when question trigger is at the beginning of sentence:
		{"What do you think is the best way to learn English?", false},
		{"What are your thoughts about Shakespeare and his plays?", false},
		// Should NOT trigger on normal words containing substrings:
		{"I went on a long journey yesterday.", false},
		{"Turning left at the corner is easy.", false},
		{"Return of the king is a great movie.", false},
		{"Hello Sarah, how are you today?", false},
	}

	for _, tc := range tests {
		result := session.matchesVoiceTrigger(tc.input)
		if result != tc.expected {
			t.Errorf("matchesVoiceTrigger(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

func TestComputePCM16RMS(t *testing.T) {
	// 1. Empty buffer
	if rms := computePCM16RMS(nil); rms != 0 {
		t.Errorf("expected 0 for nil, got %f", rms)
	}

	// 2. Pure silence (all zeros)
	silence := make([]byte, 3200) // 100ms at 16kHz
	if rms := computePCM16RMS(silence); rms != 0 {
		t.Errorf("expected 0 for silence, got %f", rms)
	}

	// 3. Constant DC signal (sample value 16384 = 0.5 amplitude)
	dc := make([]byte, 200)
	for i := 0; i < 100; i++ {
		dc[i*2] = 0x00
		dc[i*2+1] = 0x40 // 16384
	}
	rms := computePCM16RMS(dc)
	if rms < 0.49 || rms > 0.51 {
		t.Errorf("expected ~0.5 for 16384 DC, got %f", rms)
	}
}

func TestNormalizePCM16(t *testing.T) {
	// 1. Electronic floor / near-silence (peak < 120): should NOT be amplified
	floor := make([]byte, 200)
	for i := 0; i < 100; i++ {
		floor[i*2] = 50
		floor[i*2+1] = 0
	}
	normFloor := normalizePCM16(floor)
	if normFloor[0] != 50 {
		t.Errorf("expected electronic floor to remain unamplified, got %d", normFloor[0])
	}

	// 2. Quiet speech (peak = 2000): should be scaled to peak ~22000 (11.0x gain)
	quiet := make([]byte, 200)
	// sample value 2000 = 0x07D0
	quiet[0] = 0xD0
	quiet[1] = 0x07
	normQuiet := normalizePCM16(quiet)
	peakSample := int16(uint16(normQuiet[0]) | (uint16(normQuiet[1]) << 8))
	if peakSample < 21500 || peakSample > 22500 {
		t.Errorf("expected normalized peak near 22000, got %d", peakSample)
	}

	// 3. Already loud speech (peak = 24000): should NOT be amplified
	loud := make([]byte, 200)
	loud[0] = 0xC0
	loud[1] = 0x5D // 24000
	normLoud := normalizePCM16(loud)
	loudSample := int16(uint16(normLoud[0]) | (uint16(normLoud[1]) << 8))
	if loudSample != 24000 {
		t.Errorf("expected loud sample to remain untouched, got %d", loudSample)
	}
}

func TestEncodePCM16ToWAV(t *testing.T) {
	pcm := make([]byte, 3200) // 100ms at 16kHz
	wav := encodePCM16ToWAV(pcm, 16000)

	if len(wav) != 44+3200 {
		t.Fatalf("expected WAV size %d, got %d", 44+3200, len(wav))
	}

	// Check RIFF and WAVE magic headers
	if string(wav[0:4]) != "RIFF" {
		t.Errorf("expected RIFF, got %s", string(wav[0:4]))
	}
	if string(wav[8:12]) != "WAVE" {
		t.Errorf("expected WAVE, got %s", string(wav[8:12]))
	}
	if string(wav[12:16]) != "fmt " {
		t.Errorf("expected fmt , got %s", string(wav[12:16]))
	}
	if string(wav[36:40]) != "data" {
		t.Errorf("expected data, got %s", string(wav[36:40]))
	}
}
