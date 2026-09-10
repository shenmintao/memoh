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

func TestLegacyAppSteeringUsesDurablyAdmittedRun(t *testing.T) {
	f := newAdmitFixture(t)
	ctx := context.Background()
	input := make(chan turn.InjectMessage, 4)
	in := f.input("legacy-app-steer", `{"text":"initial turn"}`)
	in.Execution.InjectCh = input
	admission, err := f.manager.Admit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Handle.FencingToken <= 0 {
		t.Fatal("admission did not acquire a fencing token")
	}
	controlID := uuid.NewString()
	if _, err := f.manager.QueueSteerControl(ctx, testBotID, testSessionID, admission.RunID, controlID, "additional instruction"); err != nil {
		t.Fatal(err)
	}
	select {
	case token := <-input:
		batch, ok := token.Resolve()
		if !ok || batch.Text != "additional instruction" {
			t.Fatalf("legacy instruction was not delivered: %+v", batch)
		}
		batch.Applied()
	default:
		t.Fatal("legacy control did not wake the admitted run")
	}
	snapshot, err := f.manager.Snapshot(ctx, testBotID, testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	receipt := findQueuedSteer(snapshot.CurrentRunView, controlID)
	if receipt == nil || receipt.Status != SteerStatusApplied {
		t.Fatalf("App cannot observe consumption: %+v", receipt)
	}
	f.finish(t, admission)
	if _, err := f.manager.QueueSteerControl(ctx, testBotID, testSessionID, admission.RunID, uuid.NewString(), "too late"); err == nil {
		t.Fatal("finished run accepted another instruction")
	}
}
