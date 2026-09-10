package sessionruntime

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/felinics/memoh/internal/agent/turn"
)

const (
	maxQueuedSteers    = 32
	maxSteerReceipts   = 128
	maxSteerBatchUnits = 32000
)

func findQueuedSteer(run *CurrentRunView, id string) *SteerState {
	if run == nil || id == "" {
		return nil
	}
	for i := range run.SteerQueue {
		if run.SteerQueue[i].ID == id {
			return &run.SteerQueue[i]
		}
	}
	if run.Steer != nil && run.Steer.ID == id {
		return run.Steer
	}
	return nil
}

func syncLatestSteer(run *CurrentRunView) {
	if run != nil && len(run.SteerQueue) > 0 {
		latest := run.SteerQueue[len(run.SteerQueue)-1]
		run.Steer = &latest
	}
}

func hasPendingSteers(run *CurrentRunView) bool {
	if run == nil {
		return false
	}
	if run.Steer != nil && isPendingSteerStatus(run.Steer.Status) {
		return true
	}
	for _, item := range run.SteerQueue {
		if isPendingSteerStatus(item.Status) {
			return true
		}
	}
	return false
}

func checkSteerQueueCapacity(run *CurrentRunView, text string) error {
	entries := run.SteerQueue
	if len(entries) == 0 && run.Steer != nil {
		entries = []SteerState{*run.Steer}
	}
	if len(entries) >= maxSteerReceipts {
		return errors.New("steering receipt limit reached for this run")
	}
	count, units := 1, len(utf16.Encode([]rune(text)))
	for _, item := range entries {
		if item.Error == "steer_status_unknown" {
			return errors.New("previous steering consumption is unconfirmed")
		}
		if isPendingSteerStatus(item.Status) {
			count++
			units += len(utf16.Encode([]rune(item.Text))) + 2
		}
	}
	if count > maxQueuedSteers || units > maxSteerBatchUnits {
		return errors.New("steering queue is full")
	}
	return nil
}

// The channel contains a wake-up token. The runtime resolves it only when it
// can accept input, atomically collecting ALL currently cached messages.
func (m *Manager) dispatchSteerQueue(ctx context.Context, handle RunHandle) {
	ctrl := m.localControlForHandle(handle)
	if ctrl == nil {
		return
	}
	ctrl.steerQueueMu.Lock()
	defer ctrl.steerQueueMu.Unlock()
	var tokenID string
	_, changed, err := m.updateActiveAndPublish(ctx, handle, func(snapshot Snapshot, now time.Time) (Snapshot, bool, error) {
		run := snapshot.CurrentRunView
		if !runMatchesHandle(run, handle) || (run.Status != RunStatusRunning && run.Status != RunStatusWaitingDecision) || len(run.SteerQueue) == 0 {
			return snapshot, false, nil
		}
		for _, item := range run.SteerQueue {
			if item.Status == SteerStatusQueued || item.Error == "steer_status_unknown" {
				return snapshot, false, nil
			}
		}
		for i := range run.SteerQueue {
			item := &run.SteerQueue[i]
			if item.Status != SteerStatusPending {
				continue
			}
			tokenID = item.ID
			item.Status = SteerStatusQueued
			item.UpdatedAt = now
			snapshot.Seq++
			snapshot.UpdatedAt = now
			run.UpdatedAt = now
			syncLatestSteer(run)
			return snapshot, true, nil
		}
		return snapshot, false, nil
	}, func(snapshot Snapshot) RuntimeDelta { return runtimeRunPatch(snapshot, false, false, true, false) })
	if err != nil || !changed {
		return
	}
	token := turn.InjectMessage{ID: tokenID, Resolve: func() (turn.InjectMessage, bool) {
		return m.takeSteerBatch(context.WithoutCancel(ctx), handle, tokenID)
	}}
	if sent, reason := ctrl.sendInject(ctx, token); !sent {
		// The dispatch lock must not be acquired recursively on failure.
		m.finishSteerBatch(ctx, handle, []string{tokenID}, SteerStatusRejected, reason)
	}
}

func (m *Manager) takeSteerBatch(ctx context.Context, handle RunHandle, tokenID string) (turn.InjectMessage, bool) {
	var ids, texts []string
	_, changed, err := m.updateActiveAndPublish(ctx, handle, func(snapshot Snapshot, now time.Time) (Snapshot, bool, error) {
		run := snapshot.CurrentRunView
		marker := findQueuedSteer(run, tokenID)
		if !runMatchesHandle(run, handle) || marker == nil || marker.Status != SteerStatusQueued {
			return snapshot, false, nil
		}
		if run.Status != RunStatusRunning && run.Status != RunStatusWaitingDecision {
			return snapshot, false, nil
		}
		afterMessageID := -1
		for _, message := range run.Messages {
			afterMessageID = max(afterMessageID, message.ID)
		}
		for i := range run.SteerQueue {
			item := &run.SteerQueue[i]
			if item.ID != tokenID && item.Status != SteerStatusPending {
				continue
			}
			ids = append(ids, item.ID)
			texts = append(texts, item.Text)
			item.Status = SteerStatusQueued
			item.AfterMessageID = &afterMessageID
			item.UpdatedAt = now
		}
		snapshot.Seq++
		snapshot.UpdatedAt = now
		run.UpdatedAt = now
		syncLatestSteer(run)
		return snapshot, true, nil
	}, func(snapshot Snapshot) RuntimeDelta { return runtimeRunPatch(snapshot, false, false, true, false) })
	if err != nil || !changed {
		return turn.InjectMessage{}, false
	}
	finish := func(status, reason string) {
		m.finishSteerBatch(context.WithoutCancel(ctx), handle, ids, status, reason)
		m.dispatchSteerQueue(context.WithoutCancel(ctx), handle)
	}
	return turn.InjectMessage{
		ID: tokenID, Text: strings.Join(texts, "\n\n"),
		Applied: func() { finish(SteerStatusApplied, "") }, Rejected: func(reason string) { finish(SteerStatusRejected, reason) },
	}, true
}

func (m *Manager) finishSteerBatch(ctx context.Context, handle RunHandle, ids []string, status, reason string) {
	_, _, _ = m.updateActiveAndPublish(ctx, handle, func(snapshot Snapshot, now time.Time) (Snapshot, bool, error) {
		run := snapshot.CurrentRunView
		if !runMatchesHandle(run, handle) {
			return snapshot, false, nil
		}
		changed := false
		for _, id := range ids {
			item := findQueuedSteer(run, id)
			if item == nil || !validSteerTransition(item.Status, status) {
				continue
			}
			item.Status = status
			item.Error = reason
			item.UpdatedAt = now
			changed = true
		}
		if changed && reason == "steer_status_unknown" {
			// Later entries have never left Memoh. Reject those explicitly while
			// retaining the uncertain batch receipt; never strand or replay them.
			for i := range run.SteerQueue {
				item := &run.SteerQueue[i]
				if item.Status == SteerStatusPending {
					item.Status = SteerStatusRejected
					item.Error = "previous steering consumption is unconfirmed"
					item.UpdatedAt = now
				}
			}
		}
		if changed {
			snapshot.Seq++
			snapshot.UpdatedAt = now
			run.UpdatedAt = now
			syncLatestSteer(run)
		}
		return snapshot, changed, nil
	}, func(snapshot Snapshot) RuntimeDelta { return runtimeRunPatch(snapshot, false, false, true, false) })
}
