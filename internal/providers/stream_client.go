package providers

import (
	"encoding/json"
	"net/http"
)

func streamingBody(body []byte) bool {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	b, _ := v["stream"].(bool)
	return b
}

// clientForBody removes net/http's whole-request deadline for live streams.
// Cancellation and all connection-level timeouts still come from the request
// context / Transport. Non-stream calls keep the existing 120s safety bound.
func clientForBody(base *http.Client, body []byte) *http.Client {
	if base == nil || !streamingBody(body) {
		return base
	}
	clone := *base
	clone.Timeout = 0
	return &clone
}
