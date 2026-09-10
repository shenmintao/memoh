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

// Mirrors deepseek-harness's request-reconstruction contract over a long tool
// loop: every warm request must extend the previous one until measured pressure.
func TestProviderCachePrefixSurvivesLongToolLoopBelowPressure(t *testing.T) {
	prefix := []sdk.Message{sdk.UserMessage("complete this synthetic lookup task")}
	var previous []sdk.Message
	for cycles := 1; cycles <= 12; cycles++ {
		messages := loopSpanWithToolCycles(prefix, cycles, 6000)
		result := SelectProviderStepMessages(context.Background(), agentpkg.ContextStepSelectionInput{
			Scope: contextfrag.Scope{BotID: "cache-test"}, Messages: messages, InitialMessageCount: 1,
			BudgetMaxTokens: 100000, ProviderInputAllowanceTokens: 120000,
			KeepRecentToolResults: 4, MinMessages: 10,
		})
		if result.FatalError != nil {
			t.Fatal(result.FatalError)
		}
		request := messages
		if result.Messages != nil {
			request = result.Messages
		}
		if len(previous) > 0 && (len(request) < len(previous) || !reflect.DeepEqual(request[:len(previous)], previous)) {
			t.Fatalf("tool cycle %d rewrote the warm request prefix below the pressure threshold", cycles)
		}
		if result.Dropped != 0 || result.Truncated != 0 {
			t.Fatalf("unnecessary context mutation at cycle %d", cycles)
		}
		previous = request
	}
}

func TestSupersededBackgroundSnapshotsYieldUnderPressure(t *testing.T) {
	messages := []sdk.Message{sdk.UserMessage("task")}
	for i := 0; i < 12; i++ {
		messages = append(messages, sdk.UserMessage(contextfrag.BackgroundSummaryMessagePrefix+strings.Repeat("old task status ", 400)))
	}
	messages = append(messages, sdk.UserMessage("<message sender=\"alice\">preserve this instruction</message>"),
		sdk.UserMessage(contextfrag.BackgroundSummaryMessagePrefix+"No tasks running"))
	messages = appendToolCycles(messages, []string{"latest"}, 40)
	recentProtect := 100
	result := SelectProviderStepMessages(context.Background(), agentpkg.ContextStepSelectionInput{
		Messages: messages, InitialMessageCount: 1, BudgetMaxTokens: 2000,
		ProviderInputAllowanceTokens: 2100, RecentProtectTokens: &recentProtect,
	})
	if result.FatalError != nil || result.Dropped == 0 {
		t.Fatalf("obsolete statuses must yield instead of overflowing protected context: %+v", result)
	}
	if !selectionHasUserText(result.Messages, "preserve this instruction") || !selectionHasUserText(result.Messages, "No tasks running") {
		t.Fatal("lost the injected instruction or latest task status")
	}
}

func TestProviderCacheCheckpointIsReusedAfterPressure(t *testing.T) {
	messages := loopSpanWithToolCycles([]sdk.Message{sdk.UserMessage("task")}, 7, 6000)
	allowance := contextfrag.ProviderEnvelopeTokens("", messages, nil)
	input := agentpkg.ContextStepSelectionInput{
		Messages: messages, InitialMessageCount: 1,
		ProviderInputAllowanceTokens: allowance, KeepRecentToolResults: 2, MinMessages: 10,
	}
	first := SelectProviderStepMessages(context.Background(), input)
	if first.FatalError != nil || first.Messages == nil || first.Truncated == 0 {
		t.Fatalf("pressure must produce a smaller checkpoint: %+v", first)
	}
	// The SDK adopts prepared messages. Continue from that checkpoint, keeping
	// the immutable durable tape separate from the admitted provider view.
	input.Messages = append(append([]sdk.Message(nil), first.Messages...), sdk.UserMessage("continue"))
	next := SelectProviderStepMessages(context.Background(), input)
	if next.Messages != nil || next.Truncated != 0 || next.Dropped != 0 || next.FatalError != nil {
		t.Fatalf("next request must append to the checkpoint without rewriting it: %+v", next)
	}
}
