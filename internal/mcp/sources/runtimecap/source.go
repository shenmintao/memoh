// Package runtimecap exposes device-local MCP tools and Skills to every Bot
// through the same ToolSource contract used by native and external agents.
package runtimecap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	approval "github.com/felinics/memoh/internal/agent/decision/approval"
	"github.com/felinics/memoh/internal/mcp"
	"github.com/felinics/memoh/internal/settings"
	"github.com/felinics/memoh/internal/toolcontext"
	"github.com/felinics/memoh/internal/workspace"
)

type capabilityClient interface {
	CapabilityList(context.Context, any) error
	CapabilityCall(context.Context, any, any) error
}

type target struct {
	id, name string
	client   capabilityClient
	policy   settings.ToolApprovalConfig
}

type resolver interface {
	list(context.Context, string) ([]target, error)
	resolve(context.Context, string, string) (target, error)
}

type workspaceResolver struct{ manager *workspace.Manager }

func (r workspaceResolver) list(ctx context.Context, botID string) ([]target, error) {
	items, err := r.manager.ListWorkspaceTargets(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := make([]target, 0, len(items))
	for _, item := range items {
		if item.Kind == workspace.WorkspaceTargetRemote && item.Online {
			out = append(out, target{id: item.TargetID, name: item.Name, policy: item.ToolApprovalConfig})
		}
	}
	return out, nil
}

func (r workspaceResolver) resolve(ctx context.Context, botID, targetID string) (target, error) {
	resolved, err := r.manager.ResolveWorkspaceTarget(ctx, botID, targetID)
	if err != nil {
		return target{}, err
	}
	if resolved.Kind != workspace.WorkspaceTargetRemote {
		return target{}, errors.New("local capability requires a remote target")
	}
	return target{id: resolved.TargetID, name: resolved.Name, client: resolved.Client, policy: resolved.Approval}, nil
}

type localTool struct {
	ID          string         `json:"id"`
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type localSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type inventory struct {
	Version int          `json:"version"`
	Tools   []localTool  `json:"tools"`
	Skills  []localSkill `json:"skills"`
}

type route struct {
	targetID, targetName, kind, server, tool, skillID, path string
	descriptor                                              mcp.ToolDescriptor
}

type cacheEntry struct {
	expires   time.Time
	inventory inventory
}

type approvalService interface {
	approval.FlowService
	RegisterWaiter(string) func()
}

type Source struct {
	resolver resolver
	approval approvalService
	events   *mcp.ToolSessionContextStore
	log      *slog.Logger
	mu       sync.Mutex
	cache    map[string]cacheEntry
}

func NewSource(log *slog.Logger, manager *workspace.Manager, approvals *approval.Service, events *mcp.ToolSessionContextStore) *Source {
	source := &Source{resolver: workspaceResolver{manager}, events: events, log: log, cache: make(map[string]cacheEntry)}
	if approvals != nil {
		source.approval = approvals
	}
	return source
}

func (s *Source) ListTools(ctx context.Context, session mcp.ToolSessionContext) ([]mcp.ToolDescriptor, error) {
	routes, err := s.routes(ctx, session.BotID)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.ToolDescriptor, 0, len(routes))
	for _, item := range routes {
		out = append(out, item.descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Source) routes(ctx context.Context, botID string) (map[string]route, error) {
	if strings.TrimSpace(botID) == "" {
		return nil, errors.New("bot id is required")
	}
	targets, err := s.resolver.list(ctx, botID)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]route)
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for _, item := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			resolved, resolveErr := s.resolver.resolve(probeCtx, botID, item.id)
			if resolveErr != nil {
				return
			}
			catalog, catalogErr := s.catalog(probeCtx, botID, resolved)
			if catalogErr != nil {
				return
			} // Offline and older runtimes remain usable for exec/fs.
			mu.Lock()
			defer mu.Unlock()
			for _, tool := range catalog.Tools {
				if tool.ID == "" || tool.Name == "" || tool.InputSchema == nil {
					continue
				}
				name := alias(item.id, "mcp", tool.ID)
				routes[name] = route{targetID: item.id, targetName: item.name, kind: "mcp", server: tool.Server, tool: tool.Name, descriptor: mcp.ToolDescriptor{
					Name: name, Description: fmt.Sprintf("[%s / %s / %s] Runs on this computer. %s", item.name, tool.Server, tool.Name, tool.Description), InputSchema: capabilitySchema(item.id, "command", "MCP "+tool.Server+"/"+tool.Name, tool.InputSchema),
				}}
			}
			for _, skill := range catalog.Skills {
				if skill.ID == "" || skill.Name == "" {
					continue
				}
				name := alias(item.id, "skill", skill.ID)
				routes[name] = route{targetID: item.id, targetName: item.name, kind: "skill", skillID: skill.ID, path: skill.Path, descriptor: mcp.ToolDescriptor{
					Name: name, Description: fmt.Sprintf("Load Skill %q from %s before following its workflow: %s. Use its returned target_id for file/exec tools; scripts and paths belong to that computer.", skill.Name, item.name, skill.Description),
					InputSchema: capabilitySchema(item.id, "path", skill.Path, nil),
				}}
			}
		}()
	}
	wg.Wait()
	return routes, ctx.Err()
}

func alias(targetID, kind, localID string) string {
	hash := sha256.Sum256([]byte(targetID + "\x00" + kind + "\x00" + localID))
	return "computer_" + kind + "_" + hex.EncodeToString(hash[:12])
}

// Pin the execution target and policy input in both the native approval flow and
// the external-agent gateway. The actual MCP arguments keep their own namespace.
func capabilitySchema(targetID, field, value string, arguments map[string]any) map[string]any {
	properties := map[string]any{
		"target_id": map[string]any{"type": "string", "enum": []string{targetID}},
		field:       map[string]any{"type": "string", "enum": []string{value}},
	}
	required := []string{"target_id", field}
	if arguments != nil {
		properties["arguments"] = arguments
		required = append(required, "arguments")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (s *Source) catalog(ctx context.Context, botID string, item target) (inventory, error) {
	key := botID + "/" + item.id
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.inventory, nil
	}
	var value inventory
	if err := item.client.CapabilityList(ctx, &value); err != nil {
		return inventory{}, err
	}
	if value.Version != 1 || len(value.Tools) > 512 || len(value.Skills) > 256 {
		return inventory{}, errors.New("invalid local capability inventory")
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = make(map[string]cacheEntry)
	}
	for k, v := range s.cache {
		if time.Now().After(v.expires) {
			delete(s.cache, k)
		}
	}
	s.cache[key] = cacheEntry{expires: time.Now().Add(5 * time.Second), inventory: value}
	s.mu.Unlock()
	return value, nil
}

func (s *Source) CallTool(ctx context.Context, session mcp.ToolSessionContext, name string, args map[string]any) (map[string]any, error) {
	ctx, cancel := toolcontext.Bind(ctx, session)
	defer cancel()
	routes, err := s.routes(ctx, session.BotID)
	if err != nil {
		return nil, err
	}
	route, ok := routes[name]
	if !ok {
		return nil, mcp.ErrToolNotFound
	}
	if args["target_id"] != route.targetID {
		return nil, errors.New("target_id must match the advertised computer")
	}
	arguments := map[string]any{}
	if route.kind == "mcp" {
		if args["command"] != "MCP "+route.server+"/"+route.tool {
			return nil, errors.New("command must match the advertised MCP tool")
		}
		var valid bool
		arguments, valid = args["arguments"].(map[string]any)
		if !valid {
			return nil, errors.New("arguments must be an object")
		}
	} else if args["path"] != route.path {
		return nil, errors.New("path must match the advertised Skill")
	}
	if err := s.requireApproval(ctx, session, route, arguments); err != nil {
		return nil, err
	}
	if err := toolcontext.ValidateRuntimeGuard(ctx, session); err != nil {
		return nil, err
	}
	// Re-resolve after approval: cached metadata never grants device access.
	target, err := s.resolver.resolve(ctx, session.BotID, route.targetID)
	if err != nil {
		return nil, err
	}
	request := map[string]any{"kind": route.kind, "server": route.server, "tool": route.tool, "skill_id": route.skillID, "arguments": arguments}
	callCtx, callCancel := context.WithTimeout(ctx, 70*time.Second)
	defer callCancel()
	var result map[string]any
	if err := target.client.CapabilityCall(callCtx, request, &result); err != nil {
		return nil, err
	}
	if route.kind == "skill" {
		if structured, ok := result["structuredContent"].(map[string]any); ok {
			structured["target_id"] = route.targetID
			structured["computer"] = route.targetName
			structured["execution_note"] = "Use this target_id with exec/read/write/list for all paths and scripts in this Skill."
			return mcp.BuildToolSuccessResult(structured), nil
		}
	}
	return result, nil
}

func (s *Source) requireApproval(ctx context.Context, session mcp.ToolSessionContext, route route, args map[string]any) error {
	if s.approval == nil {
		return nil
	}
	operation := "exec"
	input := map[string]any{"target_id": route.targetID, "command": "MCP " + route.server + "/" + route.tool, "arguments": args}
	if route.kind == "skill" {
		operation = "read"
		input = map[string]any{"target_id": route.targetID, "path": route.path}
	}
	toolCallID := session.ToolCallID
	if toolCallID == "" {
		toolCallID = "local-" + uuid.NewString()
	}
	result, err := approval.RunFlow(ctx, s.approval, approval.FlowRequest{
		Input: approval.CreatePendingInput{
			BotID: session.BotID, SessionID: session.SessionID, RouteID: session.RouteID,
			ChannelIdentityID: session.ChannelIdentityID, RequestedByChannelIdentityID: session.ChannelIdentityID,
			ToolCallID: toolCallID, ToolName: operation, ToolInput: input, WorkspaceTargeted: true,
			SourcePlatform: session.CurrentPlatform, ReplyTarget: session.ReplyTarget, ConversationType: session.ConversationType,
		},
		Interactive: session.RunID != "", RegisterWaiter: s.approval.RegisterWaiter,
		Emit: func(req approval.Request) bool {
			return s.events != nil && s.events.AppendToolEvent(session, mcp.ToolStreamEvent{
				Type: "tool_approval_request", ToolCallID: toolCallID,
				ToolName: route.descriptor.Name, Input: input, ApprovalID: req.ID, ShortID: req.ShortID, Status: approval.NormalizedStatus(req.Status),
				Metadata: map[string]any{"approval": approval.RequestMetadata(req)},
			})
		},
	})
	if err != nil {
		return err
	}
	if !result.Approved {
		return fmt.Errorf("%s", approval.RejectionMessage(result))
	}
	return nil
}
