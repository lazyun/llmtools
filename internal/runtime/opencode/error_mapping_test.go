package opencode

// The two runtimes have to report the same boundary with the same code: a
// provider that refuses a model the catalogue lists is an execution limit, not
// an unavailable server, and not a missing model either.

import (
	"ai-worker-mcp/internal/agent"
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageErrorKeepsProviderAndTransportApart(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"provider error name", `{"name":"ProviderError","data":{"message":"upstream rejected"}}`, "UPSTREAM_UNAVAILABLE"},
		{"provider auth", `{"name":"ProviderAuthError","data":{"message":"invalid api key"}}`, "UPSTREAM_UNAVAILABLE"},
		{"status in message", `{"name":"APIError","data":{"message":"Request failed with status code 429"}}`, "UPSTREAM_UNAVAILABLE"},
		{"depleted quota", `{"name":"APIError","data":{"message":"402 You have depleted your monthly included credits."}}`, "UPSTREAM_UNAVAILABLE"},
		// An abort is the runtime stopping a turn, which is a transport fact about
		// this session and must not read as a provider refusal.
		{"aborted", `{"name":"MessageAbortedError","data":{"message":"aborted"}}`, "RUNTIME_UNAVAILABLE"},
		{"unknown", `{"name":"UnknownError","data":{"message":"no such model gpt-nonexistent in catalogue"}}`, "RUNTIME_UNAVAILABLE"},
		{"not an object", `"plain text"`, "RUNTIME_UNAVAILABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agent.Safe(messageError(json.RawMessage(c.raw)))
			if got.Code != c.want {
				t.Fatalf("%s: code = %q want %q (message %q)", c.name, got.Code, c.want, got.Message)
			}
			// Choosing a code from the provider's words must never carry them out.
			for _, forbidden := range []string{"invalid api key", "depleted", "gpt-nonexistent", "upstream rejected"} {
				if strings.Contains(got.Message, forbidden) {
					t.Fatalf("%s: the neutral message carried provider text: %q", c.name, got.Message)
				}
			}
		})
	}
}
