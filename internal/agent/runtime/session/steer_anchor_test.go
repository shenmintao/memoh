package sessionruntime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/felinics/memoh/internal/agent/event"
	"github.com/felinics/memoh/internal/agent/runtime/native"
	"github.com/felinics/memoh/internal/agent/turn"
)

func TestSteerBatchAnchorSurvivesOutputAndTerminalSnapshot(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "anchor-test")
	ctx := context.Background()
	input := make(chan turn.InjectMessage, 16)
	handle, err := m.StartRunHandle(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input)
	if err != nil {
		t.Fatal(err)
	}
	emit := func(kind event.StreamEventType, text string) {
		t.Helper()
		if _, err := m.HandleAgentEvent(ctx, handle, native.StreamEvent{Type: kind, Delta: text}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "supplement"); err != nil {
			t.Fatal(err)
		}
	}
	// More output can arrive while the token waits for the next runtime slot.
	emit(native.EventTextDelta, "before")
	emit(native.EventTextEnd, "")
	batch, ok := (<-input).Resolve()
	if !ok {
		t.Fatal("missing batch")
	}
	emit(native.EventTextDelta, "after")
	batch.Applied()
	if err := m.FinishRun(ctx, handle, RunStatusCompleted, ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(ctx, testBotID, testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range snapshot.CurrentRunView.SteerQueue {
		if receipt.Status != SteerStatusApplied || receipt.AfterMessageID == nil || *receipt.AfterMessageID != 0 {
			t.Fatalf("receipt lost its consumption boundary: %#v", receipt)
		}
	}
}
