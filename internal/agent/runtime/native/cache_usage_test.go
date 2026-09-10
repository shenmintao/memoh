package native

import (
	"testing"

	sdk "github.com/felinics/twilight/sdk"

	contextfrag "github.com/felinics/memoh/internal/agent/context/fragment"
)

func TestCacheUsageRecordsCompletionsHitAndMiss(t *testing.T) {
	ledger := contextfrag.NewMutationLedger()
	for i, cached := range []int{0, 800} {
		step := &sdk.StepResult{}
		step.Usage.InputTokens = 1000
		step.Usage.CachedInputTokens = cached
		recordContextCacheUsage(ledger, i, step)
	}
	usage := ledger.CacheUsageRecords()
	if len(usage) != 2 || usage[0].NoCacheTokens != 1000 || usage[1].CacheReadTokens != 800 || usage[1].NoCacheTokens != 200 {
		t.Fatalf("cold and warm requests need complete accounting: %+v", usage)
	}
}

func TestCacheUsagePreservesExplicitProviderWriteAccounting(t *testing.T) {
	ledger := contextfrag.NewMutationLedger()
	step := &sdk.StepResult{}
	step.Usage.InputTokens = 2000
	step.Usage.InputTokenDetails.CacheReadTokens = 500
	step.Usage.InputTokenDetails.CacheWriteTokens = 1500
	recordContextCacheUsage(ledger, 0, step)
	record := ledger.CacheUsageRecords()[0]
	if record.CacheReadTokens != 500 || record.CacheWriteTokens != 1500 || record.NoCacheTokens != 0 {
		t.Fatalf("cache writes must not be relabeled as misses: %+v", record)
	}
}
