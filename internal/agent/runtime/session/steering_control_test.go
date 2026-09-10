package sessionruntime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/felinics/memoh/internal/agent/turn"
)

func TestSteeringControlRejectsReplayedOldCommandAndPreservesNewInput(t *testing.T) {
	manager := testRuntimeManager(t, NewMemoryBackend(), "owner-steering-test")
	input := make(chan turn.InjectMessage, 4)
	ctx := context.Background()
	if err := manager.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	first := uuid.NewString()
	if _, err := manager.SteerControl(ctx, testBotID, testSessionID, testRunID, first, "", "first"); err != nil {
		t.Fatal(err)
	}
	message := <-input
	message.Applied()
	if _, err := manager.SteerControl(ctx, testBotID, testSessionID, testRunID, first, "", "first"); err != nil {
		t.Fatal(err)
	}
	if len(input) != 0 {
		t.Fatal("duplicate delivered")
	}
	second := uuid.NewString()
	if _, err := manager.SteerControl(ctx, testBotID, testSessionID, testRunID, second, first, "second"); err != nil {
		t.Fatal(err)
	}
	(<-input).Applied()
	if _, err := manager.SteerControl(ctx, testBotID, testSessionID, testRunID, first, "", "first"); err == nil {
		t.Fatal("old command replay accepted")
	}
	if len(input) != 0 {
		t.Fatal("old command delivered")
	}
}
