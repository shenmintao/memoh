package client

import (
	"context"
	"errors"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/felinics/memoh/internal/agent/turn"
)

// Each forwarder belongs to exactly one prompt; it never takes promptMu or
// starts another prompt. An unsupported extension rejects only the supplement.
func (*Session) forwardSteering(ctx context.Context, conn *clientConnection, sessionID acp.SessionId, runID string, input <-chan turn.InjectMessage) func() {
	if input == nil {
		return func() {}
	}
	injectCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-injectCtx.Done():
				return
			case msg, ok := <-input:
				if !ok {
					return
				}
				if msg.Resolve != nil {
					msg, ok = msg.Resolve()
					if !ok {
						continue
					}
				}
				result, err := acp.SendRequest[struct {
					Applied bool `json:"applied"`
				}](conn.conn, injectCtx, "_memoh/steer", map[string]any{
					"sessionId": sessionID, "runId": runID, "id": msg.ID, "text": msg.Text,
				})
				if err == nil && result.Applied {
					if msg.Applied != nil {
						msg.Applied()
					}
				} else if msg.Rejected != nil {
					reason := "steer_not_applied"
					if err != nil {
						reason = "steer_status_unknown"
					}
					var rpcErr *acp.RequestError
					if errors.As(err, &rpcErr) {
						switch rpcErr.Code {
						case -32601:
							reason = "steer_unsupported"
						case -32602:
							reason = "steer_not_applied"
						}
					}
					msg.Rejected(reason)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			// The adapter sends consumption receipts before resolving session/prompt.
			// Let a receipt already in transit finish before publishing the terminal run.
			select {
			case <-done:
			case <-time.After(150 * time.Millisecond):
			}
			cancel()
			<-done
		})
	}
}
