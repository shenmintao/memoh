package acpsession

import (
	"context"
	"errors"
	"testing"

	"github.com/felinics/memoh/internal/chat/thread"
	"github.com/felinics/memoh/internal/workdir"
)

type fixedWorkdir struct{ err error }

func (f fixedWorkdir) ResolveForSession(_ context.Context, botID, workdirID string) (workdir.Resolved, error) {
	if botID != "bot-1" || workdirID != "folder-1" {
		return workdir.Resolved{}, errors.New("wrong workdir scope")
	}
	return workdir.Resolved{TargetID: "computer-1"}, f.err
}

func TestSourceResolvesWorkdirTargetAndFailsClosed(t *testing.T) {
	t.Parallel()
	source := &Source{threads: fakeThreadGetter{item: thread.Thread{BotID: "bot-1", WorkdirID: "folder-1"}}, workdirs: fixedWorkdir{}}
	got, err := source.Get(context.Background(), "session-1")
	if err != nil || got.WorkspaceTargetID != "computer-1" {
		t.Fatalf("workdir target: %+v, %v", got, err)
	}
	source.workdirs = fixedWorkdir{err: workdir.ErrWorkdirNotFound}
	if _, err = source.Get(context.Background(), "session-1"); !errors.Is(err, workdir.ErrWorkdirNotFound) {
		t.Fatalf("lost workdir must not fall back: %v", err)
	}
}
