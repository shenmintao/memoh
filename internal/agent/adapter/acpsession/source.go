// Package acpsession adapts Chat thread metadata to the minimal descriptor
// consumed by the ACP runtime.
package acpsession

import (
	"context"
	"errors"

	acp "github.com/felinics/memoh/internal/agent/runtime/acp"
	"github.com/felinics/memoh/internal/chat/thread"
	"github.com/felinics/memoh/internal/workdir"
)

type Source struct {
	threads  threadGetter
	workdirs workdirResolver
}

type workdirResolver interface {
	ResolveForSession(context.Context, string, string) (workdir.Resolved, error)
}

type threadGetter interface {
	Get(ctx context.Context, sessionID string) (thread.Thread, error)
}

func NewSource(threads *thread.Service, workdirs *workdir.Service) *Source {
	return &Source{threads: threads, workdirs: workdirs}
}

func (s *Source) Get(ctx context.Context, sessionID string) (acp.SessionDescriptor, error) {
	if s == nil || s.threads == nil {
		return acp.SessionDescriptor{}, errors.New("thread service unavailable")
	}
	item, err := s.threads.Get(ctx, sessionID)
	if err != nil {
		return acp.SessionDescriptor{}, err
	}
	descriptor := acp.SessionDescriptor{
		BotID:           item.BotID,
		SessionType:     item.Type,
		Metadata:        item.Metadata,
		RuntimeMetadata: item.RuntimeMetadata,
		IsACP:           thread.IsACPRuntime(item),
	}
	if item.WorkdirID != "" {
		if s.workdirs == nil {
			return acp.SessionDescriptor{}, errors.New("session workdir resolver unavailable")
		}
		bound, err := s.workdirs.ResolveForSession(ctx, item.BotID, item.WorkdirID)
		if err != nil {
			return acp.SessionDescriptor{}, err
		}
		descriptor.WorkspaceTargetID = bound.TargetID
	}
	return descriptor, nil
}
