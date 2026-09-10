package acp

import (
	"context"
	"errors"
	"testing"

	"github.com/felinics/memoh/internal/agent/runtime/acp/client"
	"github.com/felinics/memoh/internal/workspace"
	"github.com/felinics/memoh/internal/workspace/bridge"
)

type workdirDescriptor struct{}

func (workdirDescriptor) Get(context.Context, string) (SessionDescriptor, error) {
	return SessionDescriptor{BotID: "bot-1", IsACP: true, WorkspaceTargetID: "computer-1"}, nil
}

type workdirRunner struct{ target string }

func (r *workdirRunner) WorkspaceInfo(ctx context.Context, _ string) (bridge.WorkspaceInfo, error) {
	r.target = workspace.WorkspaceTargetFromContext(ctx)
	return bridge.WorkspaceInfo{}, errors.New("probe completed")
}

func (*workdirRunner) StartSession(context.Context, client.StartRequest, client.EventSink) (*client.Session, error) {
	return nil, errors.New("must resolve workspace before starting")
}

func TestColdStartUsesPersistedWorkdirInsteadOfPrimaryTarget(t *testing.T) {
	t.Parallel()
	runner := &workdirRunner{}
	pool := newSessionPool(nil, runner, fakeBotGetter{bot: enabledACPBot("bot-1", "self", nil)})
	pool.store = workdirDescriptor{}
	handle := &runtimeHandle{id: "runtime-1", botID: "bot-1", agentID: "codex", boundSession: "session-1"}
	ctx := workspace.WithWorkspaceTarget(context.Background(), workspace.WorkspaceTargetNative)
	if err := pool.startRuntime(ctx, handle, startOptions{}); err == nil {
		t.Fatal("expected probe to stop startup")
	}
	if runner.target != "computer-1" {
		t.Fatalf("workspace resolved on %q, want bound computer", runner.target)
	}
}
