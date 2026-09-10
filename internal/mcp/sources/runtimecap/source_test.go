package runtimecap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/felinics/memoh/internal/mcp"
)

type fakeClient struct {
	catalog inventory
	calls   int
	last    map[string]any
	err     error
}

func (f *fakeClient) CapabilityList(_ context.Context, result any) error {
	if f.err != nil {
		return f.err
	}
	*result.(*inventory) = f.catalog
	return nil
}

func (f *fakeClient) CapabilityCall(ctx context.Context, request, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.calls++
	f.last = request.(map[string]any)
	raw := []byte(`{"content":[{"type":"text","text":"ok"}],"structuredContent":{"instructions":"test"}}`)
	return json.Unmarshal(raw, result)
}

type fakeResolver struct {
	targets map[string][]target
	denied  map[string]bool
}

func (f *fakeResolver) list(_ context.Context, bot string) ([]target, error) {
	return f.targets[bot], nil
}

func (f *fakeResolver) resolve(_ context.Context, bot, id string) (target, error) {
	if !f.denied[id] {
		for _, item := range f.targets[bot] {
			if item.id == id {
				return item, nil
			}
		}
	}
	return target{}, errors.New("unbound or offline")
}

func fixture() (*Source, *fakeResolver, *fakeClient, *fakeClient) {
	catalog := inventory{Version: 1, Tools: []localTool{{ID: "tool", Server: "ha", Name: "state", InputSchema: map[string]any{"type": "object"}}}, Skills: []localSkill{{ID: "skill", Name: "ha", Path: "/skills/ha/SKILL.md"}}}
	a, b := &fakeClient{catalog: catalog}, &fakeClient{catalog: catalog}
	r := &fakeResolver{targets: map[string][]target{"bot-a": {{id: "nas", name: "NAS", client: a}, {id: "pc", name: "PC", client: b}}, "bot-b": {{id: "pc", name: "PC", client: b}}}, denied: map[string]bool{}}
	return &Source{resolver: r}, r, a, b
}

func callArgs(id string) map[string]any {
	return map[string]any{"target_id": id, "command": "MCP ha/state", "arguments": map[string]any{"entity": "light.test"}}
}

func TestDiscoveryRoutesSameNamedToolsToTheirComputer(t *testing.T) {
	s, _, a, b := fixture()
	ctx := context.Background()
	session := mcp.ToolSessionContext{BotID: "bot-a"}
	list, err := s.ListTools(ctx, session)
	if err != nil || len(list) != 4 {
		t.Fatalf("list: %v %v", list, err)
	}
	_, err = s.CallTool(ctx, session, alias("nas", "mcp", "tool"), callArgs("nas"))
	if err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || b.calls != 0 || a.last["tool"] != "state" {
		t.Fatalf("wrong route: %+v %+v", a, b)
	}
}

func TestCachedInventoryCannotGrantAnotherBotAccess(t *testing.T) {
	s, _, a, _ := fixture()
	ctx := context.Background()
	if _, err := s.ListTools(ctx, mcp.ToolSessionContext{BotID: "bot-a"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.CallTool(ctx, mcp.ToolSessionContext{BotID: "bot-b"}, alias("nas", "mcp", "tool"), callArgs("nas"))
	if !errors.Is(err, mcp.ErrToolNotFound) || a.calls != 0 {
		t.Fatalf("cross-bot call: %v %d", err, a.calls)
	}
}

func TestOfflineOrRemovedDeviceCannotExecuteCachedTool(t *testing.T) {
	s, r, a, _ := fixture()
	session := mcp.ToolSessionContext{BotID: "bot-a"}
	ctx := context.Background()
	if _, err := s.ListTools(ctx, session); err != nil {
		t.Fatal(err)
	}
	r.denied["nas"] = true
	_, err := s.CallTool(ctx, session, alias("nas", "mcp", "tool"), callArgs("nas"))
	if !errors.Is(err, mcp.ErrToolNotFound) || a.calls != 0 {
		t.Fatalf("revoked call: %v", err)
	}
}

func TestForgedApprovalTargetOrCommandDoesNotExecute(t *testing.T) {
	for _, field := range []string{"target_id", "command", "arguments"} {
		t.Run(field, func(t *testing.T) {
			s, _, a, _ := fixture()
			args := callArgs("nas")
			args[field] = "forged"
			_, err := s.CallTool(context.Background(), mcp.ToolSessionContext{BotID: "bot-a"}, alias("nas", "mcp", "tool"), args)
			if err == nil || a.calls != 0 {
				t.Fatalf("forgery executed: %v", err)
			}
		})
	}
}

func TestSkillReturnsItsExecutionTargetAndRejectsForgedPath(t *testing.T) {
	s, _, a, _ := fixture()
	session := mcp.ToolSessionContext{BotID: "bot-a"}
	args := map[string]any{"target_id": "nas", "path": "/skills/ha/SKILL.md"}
	result, err := s.CallTool(context.Background(), session, alias("nas", "skill", "skill"), args)
	if err != nil {
		t.Fatal(err)
	}
	if result["structuredContent"].(map[string]any)["target_id"] != "nas" {
		t.Fatal(result)
	}
	args["path"] = "/safe"
	_, err = s.CallTool(context.Background(), session, alias("nas", "skill", "skill"), args)
	if err == nil || a.calls != 1 {
		t.Fatalf("forged path: %v", err)
	}
}

func TestOldRuntimeDoesNotHideHealthyComputer(t *testing.T) {
	s, _, a, _ := fixture()
	a.err = errors.New("unimplemented")
	list, err := s.ListTools(context.Background(), mcp.ToolSessionContext{BotID: "bot-a"})
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
}
