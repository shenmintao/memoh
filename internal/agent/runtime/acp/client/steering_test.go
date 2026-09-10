package client

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/felinics/memoh/internal/agent/turn"
)

func TestSteeringUsesSeparateBoundRPCAndWaitsForConsumption(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer func() { _ = clientRead.Close() }()
	defer func() { _ = serverWrite.Close() }()
	defer func() { _ = serverRead.Close() }()
	defer func() { _ = clientWrite.Close() }()
	seen := make(chan map[string]string, 1)
	consume := make(chan struct{})
	peer := acp.NewConnection(func(ctx context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
		if method != "_memoh/steer" {
			t.Errorf("unexpected method %s", method)
		}
		var request map[string]string
		if err := json.Unmarshal(params, &request); err != nil {
			t.Error(err)
		}
		seen <- request
		select {
		case <-consume:
		case <-ctx.Done():
		}
		return map[string]bool{"applied": true}, nil
	}, serverWrite, serverRead)
	_ = peer
	conn := newClientConnection(nil, clientWrite, clientRead)
	input := make(chan turn.InjectMessage, 1)
	applied := make(chan struct{}, 1)
	stop := (&Session{}).forwardSteering(context.Background(), conn, "session", "run", input)
	defer stop()
	input <- turn.InjectMessage{Resolve: func() (turn.InjectMessage, bool) {
		return turn.InjectMessage{ID: "id", Text: "first\n\nsecond\n\nthird", Applied: func() { applied <- struct{}{} }}, true
	}}
	select {
	case request := <-seen:
		if request["text"] != "first\n\nsecond\n\nthird" {
			t.Errorf("batch text = %q", request["text"])
		}
		if request["sessionId"] != "session" || request["runId"] != "run" || request["id"] != "id" {
			t.Fatalf("bad scope: %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("steering was blocked behind prompt")
	}
	select {
	case <-applied:
		t.Fatal("acknowledged before consumption")
	default:
	}
	close(consume)
	select {
	case <-applied:
	case <-time.After(time.Second):
		t.Fatal("missing applied receipt")
	}
	close(input)
}

func TestSteeringUnsupportedIsNotAcknowledged(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer func() { _ = clientRead.Close() }()
	defer func() { _ = serverWrite.Close() }()
	defer func() { _ = serverRead.Close() }()
	defer func() { _ = clientWrite.Close() }()
	peer := acp.NewConnection(func(context.Context, string, json.RawMessage) (any, *acp.RequestError) {
		return nil, &acp.RequestError{Code: -32601, Message: "not supported"}
	}, serverWrite, serverRead)
	_ = peer
	input := make(chan turn.InjectMessage, 1)
	rejected := make(chan string, 1)
	stop := (&Session{}).forwardSteering(context.Background(), newClientConnection(nil, clientWrite, clientRead), "session", "run", input)
	defer stop()
	input <- turn.InjectMessage{ID: "id", Text: "supplement", Applied: func() { t.Error("unsupported runtime acknowledged") }, Rejected: func(reason string) { rejected <- reason }}
	select {
	case reason := <-rejected:
		if reason != "steer_unsupported" {
			t.Fatalf("reason = %s", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("missing rejection")
	}
	close(input)
}
