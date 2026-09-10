package server

import "testing"

func TestRuntimeMCPJWTExceptionIsExact(t *testing.T) {
	if !shouldSkipJWT("/bots/bot-1/runtime-tools") {
		t.Fatal("runtime endpoint authenticates its own scoped token")
	}
	for _, path := range []string{
		"/bots/bot-1/tools", "/bots/bot-1", "/bots/bot-1/runtime-tools/extra",
		"/bots/bot-1/runtime-tools/", "/bots//runtime-tools", "/api/bots/bot-1/runtime-tools",
		"/bots/bot-1/runtime-tools-other", "/bots/bot-1/sessions",
	} {
		if shouldSkipJWT(path) {
			t.Errorf("%q unexpectedly skips account authentication", path)
		}
	}
}
