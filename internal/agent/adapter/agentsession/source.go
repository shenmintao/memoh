// Package agentsession adapts Chat thread metadata to the minimal descriptor
// consumed by the ACP runtime.
package agentsession

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
	MergeRuntimeMetadata(ctx context.Context, sessionID, runtimeType string, delta map[string]any) (thread.Thread, error)
}

func NewSource(threads *thread.Service, workdirs ...*workdir.Service) *Source {
	source := &Source{threads: threads}
	if len(workdirs) > 0 {
		source.workdirs = workdirs[0]
	}
	return source
}

func (s *Source) Get(ctx context.Context, sessionID string) (acp.SessionDescriptor, error) {
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

// SaveModelPreference is called under the ACP runtime operation lock, so
// an earlier setter cannot persist its state after a later setter or prompt.
func (s *Source) SaveModelPreference(ctx context.Context, sessionID, modelID, effort string) error {
	var model, reasoning any
	if modelID != "" {
		model = modelID
	}
	if effort != "" {
		reasoning = effort
	}
	_, err := s.threads.MergeRuntimeMetadata(ctx, sessionID, thread.RuntimeACPAgent, map[string]any{"acp_model_id": model, "acp_reasoning_effort": reasoning})
	return err
}
