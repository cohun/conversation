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
