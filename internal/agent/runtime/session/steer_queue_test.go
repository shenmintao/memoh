package sessionruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/felinics/memoh/internal/agent/turn"
)

func TestSteerQueueDrainsAllCachedTextAtRuntimeBoundary(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "queue-test")
	input := make(chan turn.InjectMessage, 16)
	ctx := context.Background()
	if err := m.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, text := range []string{"first", "second", "third"} {
		if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, ids[i], text); err != nil {
			t.Fatal(err)
		}
	}
	if len(input) != 1 {
		t.Fatalf("tokens=%d", len(input))
	}
	token := <-input
	batch, ok := token.Resolve()
	if !ok || batch.Text != "first\n\nsecond\n\nthird" {
		t.Fatalf("batch=%#v", batch)
	}
	fourth := uuid.NewString()
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, fourth, "fourth"); err != nil {
		t.Fatal(err)
	}
	if len(input) != 0 {
		t.Fatal("dispatched before first consumption")
	}
	batch.Applied()
	snapshot, _ := m.Snapshot(ctx, testBotID, testSessionID)
	for _, id := range ids {
		if findQueuedSteer(snapshot.CurrentRunView, id).Status != SteerStatusApplied {
			t.Fatal("missing per-message receipt")
		}
	}
	next, ok := (<-input).Resolve()
	if !ok || next.Text != "fourth" {
		t.Fatal("next batch lost")
	}
	next.Applied()
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, ids[0], "first"); err != nil {
		t.Fatal(err)
	}
	if len(input) != 0 {
		t.Fatal("duplicate replayed")
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, ids[0], "changed"); err == nil {
		t.Fatal("conflicting id accepted")
	}
}

func TestSteerQueueRejectsWholeBatchAndNeverLeaksAfterStop(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "queue-stop")
	input := make(chan turn.InjectMessage, 16)
	ctx := context.Background()
	if err := m.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two"} {
		if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), text); err != nil {
			t.Fatal(err)
		}
	}
	batch, ok := (<-input).Resolve()
	if !ok {
		t.Fatal("not resolved")
	}
	batch.Rejected("steer_unsupported")
	snapshot, _ := m.Snapshot(ctx, testBotID, testSessionID)
	for _, item := range snapshot.CurrentRunView.SteerQueue {
		if item.Status != SteerStatusRejected {
			t.Fatal("not rejected")
		}
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "stop before consume"); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishRun(ctx, RunHandle{BotID: testBotID, SessionID: testSessionID, RunID: testRunID, Generation: snapshot.CurrentRunView.Generation}, RunStatusAborted, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := (<-input).Resolve(); ok {
		t.Fatal("stopped queue consumed")
	}
}

func TestSteerQueueBoundsCombinedBatchSize(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "queue-size")
	input := make(chan turn.InjectMessage, 16)
	ctx := context.Background()
	if err := m.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), strings.Repeat("a", 32000)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "b"); err == nil {
		t.Fatal("oversized pending batch accepted")
	}
}

func TestSteerQueueConcurrentAdmissionPreservesEveryMessage(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "concurrent-queue")
	input := make(chan turn.InjectMessage, 16)
	ctx := context.Background()
	if err := m.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), fmt.Sprintf("message-%d", i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(input) != 1 {
		t.Fatalf("tokens=%d", len(input))
	}
	snapshot, _ := m.Snapshot(ctx, testBotID, testSessionID)
	expected := make([]string, 0, 20)
	for _, item := range snapshot.CurrentRunView.SteerQueue {
		expected = append(expected, item.Text)
	}
	batch, ok := (<-input).Resolve()
	if !ok || len(expected) != 20 || batch.Text != strings.Join(expected, "\n\n") {
		t.Fatal("batch lost admission order or content")
	}
	batch.Applied()
	snapshot, _ = m.Snapshot(ctx, testBotID, testSessionID)
	for _, item := range snapshot.CurrentRunView.SteerQueue {
		if item.Status != SteerStatusApplied {
			t.Fatal("missing receipt")
		}
	}
}

func TestSteerQueueUnknownBatchRejectsUnsentCacheWithoutReplay(t *testing.T) {
	m := testRuntimeManager(t, NewMemoryBackend(), "queue-unknown")
	input := make(chan turn.InjectMessage, 16)
	ctx := context.Background()
	if err := m.StartRun(ctx, testBotID, testSessionID, testRunID, make(chan struct{}, 1), func() {}, input); err != nil {
		t.Fatal(err)
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "first"); err != nil {
		t.Fatal(err)
	}
	batch, ok := (<-input).Resolve()
	if !ok {
		t.Fatal("missing batch")
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "later"); err != nil {
		t.Fatal(err)
	}
	batch.Rejected("steer_status_unknown")
	snapshot, _ := m.Snapshot(ctx, testBotID, testSessionID)
	items := snapshot.CurrentRunView.SteerQueue
	if items[0].Error != "steer_status_unknown" || items[1].Status != SteerStatusRejected || items[1].Error == "steer_status_unknown" {
		t.Fatalf("bad uncertainty scope: %#v", items)
	}
	if _, err := m.QueueSteerControl(ctx, testBotID, testSessionID, testRunID, uuid.NewString(), "third"); err == nil {
		t.Fatal("accepted into blocked runtime")
	}
	if len(input) != 0 {
		t.Fatal("replayed after uncertain consumption")
	}
}
