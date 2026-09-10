package view

import (
	"encoding/json"
	"testing"

	messagepkg "github.com/felinics/memoh/internal/chat/message"
)

func TestSupplementHistoryPreservesRunIdentityAcrossTurns(t *testing.T) {
	rows := []messagepkg.Message{
		{ID: "u1", TurnID: "first", RunID: "run", Role: "user", DisplayContent: "start"},
		{ID: "a1", TurnID: "first", RunID: "run", Role: "assistant", Content: json.RawMessage(`"before"`)},
		{ID: "u2", TurnID: "second", RunID: "run", Role: "user", DisplayContent: "supplement"},
		{ID: "a2", TurnID: "second", RunID: "run", Role: "assistant", Content: json.RawMessage(`"after"`)},
	}
	turns := ConvertMessagesToUITurns(rows)
	if len(turns) != 4 {
		t.Fatalf("turns=%#v", turns)
	}
	for _, turn := range turns {
		if turn.RunID != "run" {
			t.Fatalf("missing run identity: %#v", turn)
		}
	}
}
