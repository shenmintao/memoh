package reasoning

import (
	"slices"
	"testing"
)

func TestMaxIsModelSpecificAndPreservedAcrossClients(t *testing.T) {
	for _, client := range []string{"openai-completions", "openai-responses", "openai-codex", ClientTypeAnthropicMessages} {
		t.Run(client, func(t *testing.T) {
			levels := []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
			opts := OptionsFor(ModeToggle, levels, client, "")
			if !slices.Contains(opts.Efforts, EffortMax) {
				t.Fatal(opts)
			}
			for _, pair := range [][2]string{{EffortMax, ""}, {EffortLow, EffortMax}} {
				cfg := ResolveConfig(ModeToggle, levels, opts, pair[0], pair[1], client)
				if cfg == nil || cfg.Effort != EffortMax {
					t.Fatalf("max changed: %+v", cfg)
				}
			}
			fallback := OptionsFor(ModeToggle, nil, client, "")
			if slices.Contains(fallback.Efforts, EffortMax) {
				t.Fatal("undeclared model offered max")
			}
			cfg := ResolveConfig(ModeToggle, nil, fallback, "", EffortMax, client)
			if cfg == nil || cfg.Effort == EffortMax {
				t.Fatalf("undeclared max sent: %+v", cfg)
			}
		})
	}
}
