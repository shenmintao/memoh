package view_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/felinics/memoh/internal/agent/event"
	"github.com/felinics/memoh/internal/agent/runtime/acp/client"
	"github.com/felinics/memoh/internal/agent/view"
)

func TestACPThoughtSegmentsStayInPlaceAtCompletion(t *testing.T) {
	recorder := client.NewTranscriptRecorder()
	converter := view.NewUIMessageStreamConverter()
	live := map[int]view.UIMessage{}
	emit := func(ev event.StreamEvent) {
		recorder.Add(ev)
		payload, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var uiEvent view.UIMessageStreamEvent
		if err := json.Unmarshal(payload, &uiEvent); err != nil {
			t.Fatal(err)
		}
		for _, block := range converter.HandleEvent(uiEvent) {
			live[block.ID] = block
		}
	}
	// ACP sends thought chunks without reasoning_start/reasoning_end. The
	// transcript still separates phases at tool calls and subsequent text.
	for i := range 20 {
		emit(event.StreamEvent{Type: event.ReasoningDelta, Delta: fmt.Sprintf("phase-%d", i)})
		emit(event.StreamEvent{Type: event.ReasoningDelta, Delta: " continued"})
		emit(event.StreamEvent{Type: event.ToolCallStart, ToolCallID: fmt.Sprintf("call-%d", i), ToolName: "lookup"})
		emit(event.StreamEvent{Type: event.ToolCallEnd, ToolCallID: fmt.Sprintf("call-%d", i), ToolName: "lookup", Result: map[string]any{"ok": true}})
	}
	emit(event.StreamEvent{Type: event.ReasoningDelta, Delta: "final phase"})
	emit(event.StreamEvent{Type: event.TextDelta, Delta: "Final answer"})
	raw, err := json.Marshal(recorder.Messages(""))
	if err != nil {
		t.Fatal(err)
	}
	terminal := converter.ConvertTerminalMessages(raw)
	for _, block := range terminal {
		prior, exists := live[block.ID]
		if !exists {
			t.Errorf("completion appended unseen %s block %d", block.Type, block.ID)
			continue
		}
		if prior.Type != block.Type || prior.Content != block.Content {
			t.Errorf("completion relocated %s block %d", block.Type, block.ID)
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].ID < terminal[j].ID })
	if last := terminal[len(terminal)-1]; last.Type != view.UIMessageText || last.Content != "Final answer" {
		t.Errorf("final answer displaced by %s", last.Type)
	}
	if len(live) != len(terminal) {
		t.Errorf("block count jumped from %d to %d", len(live), len(terminal))
	}
}

func TestThoughtTextAndSupplementBoundariesKeepDistinctBlocks(t *testing.T) {
	events := []event.StreamEvent{
		{Type: event.TextDelta, Delta: "progress"},
		{Type: event.ReasoningDelta, Delta: "phase one"},
		{Type: event.InjectedUserMessage, Delta: "supplement"},
		{Type: event.ReasoningDelta, Delta: "phase two"},
		{Type: event.TextDelta, Delta: "answer"},
	}
	recorder := client.NewTranscriptRecorder()
	converter := view.NewUIMessageStreamConverter()
	live := map[int]view.UIMessage{}
	for _, ev := range events {
		recorder.Add(ev)
		data, _ := json.Marshal(ev)
		var uiEvent view.UIMessageStreamEvent
		if err := json.Unmarshal(data, &uiEvent); err != nil {
			t.Fatal(err)
		}
		for _, block := range converter.HandleEvent(uiEvent) {
			live[block.ID] = block
		}
	}
	raw, err := json.Marshal(recorder.Messages(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range converter.ConvertTerminalMessages(raw) {
		if old, ok := live[block.ID]; !ok || old.Type != block.Type || old.Content != block.Content {
			t.Errorf("boundary changed at completion: %#v", block)
		}
	}
	if len(live) != 4 {
		t.Errorf("got %d blocks, want progress, two thought phases and answer", len(live))
	}
}

func TestLateToolMetadataDoesNotSplitReasoning(t *testing.T) {
	converter := view.NewUIMessageStreamConverter()
	converter.HandleEvent(view.UIMessageStreamEvent{Type: "tool_call_start", ToolCallID: "background", ToolName: "lookup"})
	first := converter.HandleEvent(view.UIMessageStreamEvent{Type: "reasoning_delta", Delta: "phase "})
	converter.HandleEvent(view.UIMessageStreamEvent{Type: "tool_call_metadata", ToolCallID: "background", ToolName: "lookup"})
	next := converter.HandleEvent(view.UIMessageStreamEvent{Type: "reasoning_delta", Delta: "continued"})
	if len(first) != 1 || len(next) != 1 || first[0].ID != next[0].ID || next[0].Content != "phase continued" {
		t.Fatal("tool metadata created an extra thought block")
	}
}
