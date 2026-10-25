package agent

import (
	"strings"
	"testing"
)

// runtimeNames are the words that would make a durable, user-facing error
// message describe one runtime instead of the boundary that actually failed.
// Operators see these strings in a parent agent's transcript, where naming a
// product would leak the execution stack and confuse a runtime-neutral run.
var runtimeNames = []string{"OpenCode", "opencode", "Pi", "StructuredOutput"}

// TestErrorVocabularyIsRuntimeNeutral is the user-facing half of the parity
// gate: every code Core can persist must read the same way whichever runtime
// served the run.
func TestErrorVocabularyIsRuntimeNeutral(t *testing.T) {
	if len(messages) == 0 {
		t.Fatal("error vocabulary is empty")
	}
	for code, message := range messages {
		for _, word := range runtimeNames {
			if strings.Contains(message, word) {
				t.Fatalf("%s message names %q: %s", code, word, message)
			}
		}
		if strings.TrimSpace(message) == "" {
			t.Fatalf("%s has no message", code)
		}
	}
}

// TestUpstreamAndCatalogueAreDistinctCodes pins the classification the parity
// round exists for: a model missing from the catalogue and a provider that
// refuses to serve an existing model must never collapse into one code.
func TestUpstreamAndCatalogueAreDistinctCodes(t *testing.T) {
	missing, upstream := messages["MODEL_NOT_AVAILABLE"], messages["UPSTREAM_UNAVAILABLE"]
	if missing == "" || upstream == "" || missing == upstream {
		t.Fatalf("catalogue absence and upstream refusal are not distinct: %q %q", missing, upstream)
	}
	if !strings.Contains(missing, "not currently available in the selected runtime") {
		t.Fatalf("MODEL_NOT_AVAILABLE is not runtime-neutral: %s", missing)
	}
	for _, code := range []string{"RUNTIME_UNAVAILABLE", "RUNTIME_PROTOCOL_ERROR"} {
		if messages[code] == "" || messages[code] == upstream || messages[code] == missing {
			t.Fatalf("%s must stay distinguishable from model availability", code)
		}
	}
}

// TestUpstreamBlockDetectionIsShared keeps the provider-refusal rule next to the
// neutral vocabulary it selects a code from, instead of letting each adapter grow
// its own copy that can drift: the same refusal has to classify the same way for
// every runtime, and a runtime fault must never read as one.
func TestUpstreamBlockDetectionIsShared(t *testing.T) {
	for _, text := range []string{
		"401 unauthorized",
		"402 you have depleted your monthly included credits",
		"request failed with status code 429",
		"invalid api key",
		"provider temporarily unavailable",
	} {
		if !MentionsUpstreamBlock(text) {
			t.Fatalf("provider refusal was not detected: %s", text)
		}
	}
	for _, text := range []string{"connection refused", "session lost", "no assistant message", "messageabortederror"} {
		if MentionsUpstreamBlock(text) {
			t.Fatalf("runtime fault read as a provider refusal: %s", text)
		}
	}
}
