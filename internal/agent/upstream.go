package agent

import (
	"regexp"
	"strings"
)

// A model can be refused at the same boundary whichever runtime served the turn:
// a provider that answers 401, 402 or 429 for a model the catalogue lists has
// neither a missing model nor an unavailable runtime. Each adapter feeds its own
// error text through this one rule so the same boundary produces the same
// runtime-neutral code.
var upstreamMarkers = []string{"unauthorized", "forbidden", "authentication", "invalid api key", "api key", "provider", "quota", "credit", "payment required", "insufficient", "budget", "rate limit", "temporarily unavailable", "pre-dispatch"}

// upstreamStatus matches the HTTP statuses a provider or gateway answers with
// when it refuses to serve a model that exists. The word boundaries keep a model
// name or a byte count from reading as a status code.
var upstreamStatus = regexp.MustCompile(`\b(?:40[1-9]|429|50[0-4])\b`)

// MentionsUpstreamBlock reports whether lower-cased error text names a provider
// refusing to serve an existing model, as opposed to a runtime or protocol
// fault. The text only selects the code: it is never part of the result, because
// a provider message can echo a credential back.
func MentionsUpstreamBlock(lower string) bool {
	if upstreamStatus.MatchString(lower) {
		return true
	}
	for _, marker := range upstreamMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
