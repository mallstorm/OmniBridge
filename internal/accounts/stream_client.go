package accounts

import (
	"encoding/json"
	"net/http"
)

func clientForStreamingBody(base *http.Client, body []byte) *http.Client {
	if base == nil {
		return base
	}
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return base
	}
	stream, _ := v["stream"].(bool)
	if !stream {
		return base
	}
	clone := *base
	clone.Timeout = 0
	return &clone
}
