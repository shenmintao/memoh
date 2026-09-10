package contextview

import (
	"context"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/felinics/twilight/sdk"

	contextfrag "github.com/felinics/memoh/internal/agent/context/fragment"
	agentpkg "github.com/felinics/memoh/internal/agent/runtime/native"
)

func TestStepReselectionLongTaskAfterSteeringRetainsInstructionsAndLatestClosure(t *testing.T) {
	t.Parallel()
	prefix := []sdk.Message{sdk.UserMessage("original task, keep this unchanged")}
	const instruction = "Continue the current task and retain the requested output format."
	messages := appendToolCycles(append([]sdk.Message(nil), prefix...), []string{"before"}, 100)
	messages = append(messages, sdk.UserMessage(instruction))
	for _, id := range []string{"old-a", "old-b", "latest"} {
		call := assistantToolCallMessage(id, "lookup", "")
		call.Content = append([]sdk.MessagePart{sdk.ReasoningPart{Text: strings.Repeat("r", 7000)}}, call.Content...)
		messages = append(messages, call, toolResultMessage(id, "lookup", strings.Repeat("x", 800)))
	}
	messages = append(messages, backgroundSummaryMessageForTest())
	before := cloneSDKMessages(messages)
	const allowance = 5000
	result := SelectProviderStepMessages(context.Background(), agentpkg.ContextStepSelectionInput{
		Scope: contextfrag.Scope{BotID: "long-task"}, InitialMessageCount: len(prefix), Messages: messages,
		BudgetMaxTokens:              allowance - contextfrag.ProviderEnvelopeTokens("", prefix, nil),
		ProviderInputAllowanceTokens: allowance,
	})
	if result.FatalError != nil {
		t.Fatalf("completed work after steering must yield under pressure: %v", result.FatalError)
	}
	if result.Dropped == 0 || result.Messages == nil {
		t.Fatalf("expected bounded pruning: %+v", result)
	}
	if !reflect.DeepEqual(result.Messages[:len(prefix)], prefix) || !reflect.DeepEqual(messages, before) {
		t.Fatal("prefix or durable input messages changed")
	}
	if !selectionHasUserText(result.Messages, instruction) || !selectionHasUserText(result.Messages, "[Background tasks]") {
		t.Fatal("steering instruction or background summary was dropped")
	}
	if !selectionHasToolResult(result.Messages, "latest") || selectionHasToolResult(result.Messages, "old-a") {
		t.Fatal("expected latest result intact and older completed cycle removed")
	}
	for _, original := range messages[len(messages)-3:] {
		found := false
		for _, selected := range result.Messages {
			if reflect.DeepEqual(original, selected) {
				found = true
			}
		}
		if !found {
			t.Fatal("latest reasoning/call/result or background status was modified")
		}
	}
	if got := contextfrag.ProviderEnvelopeTokens("", result.Messages, nil); got > allowance {
		t.Fatalf("provider context remains oversized: %d > %d", got, allowance)
	}
	for _, message := range result.Messages {
		for _, part := range message.Content {
			if call, ok := part.(sdk.ToolCallPart); ok && !selectionHasToolResult(result.Messages, call.ToolCallID) {
				t.Fatalf("orphaned tool call %s", call.ToolCallID)
			}
		}
	}
	if !result.MessageSourceIndexesKnown {
		t.Fatal("message provenance missing")
	}
	for index, source := range result.MessageSourceIndexes {
		if source >= 0 && !reflect.DeepEqual(result.Messages[index], messages[source]) {
			t.Fatal("selected message origin is incorrect")
		}
	}
}

func backgroundSummaryMessageForTest() sdk.Message {
	return sdk.UserMessage("[Background tasks]\nCurrently running background tasks:\n- [task-1] build (started 3s ago)")
}
