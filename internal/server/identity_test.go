package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
)

func TestInjectModelIdentityAllProtocols(t *testing.T) {
	m := providers.Model{ID: "ya/gpt://folder/deepseek-v4-flash", NativeID: "gpt://folder/deepseek-v4-flash", ProviderID: "ya", ProviderName: "Yandex AI Studio", Name: "DeepSeek V4 Flash"}
	cases := []struct {
		apiType string
		body    string
	}{
		{protocol.ChatCompletions, `{"model":"auto/smart","messages":[{"role":"system","content":"host"},{"role":"user","content":"who are you"}]}`},
		{protocol.Responses, `{"model":"auto/smart","instructions":"host","input":"who are you"}`},
		{protocol.Messages, `{"model":"auto/smart","system":"host","messages":[{"role":"user","content":"who are you"}]}`},
	}
	for _, tc := range cases {
		out := injectModelIdentity([]byte(tc.body), tc.apiType, m)
		if !json.Valid(out) {
			t.Fatalf("%s returned invalid JSON: %s", tc.apiType, out)
		}
		text := string(out)
		for _, required := range []string{"DeepSeek V4 Flash", "Yandex AI Studio", "Do not identify yourself as GitHub Copilot"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s identity missing %q: %s", tc.apiType, required, text)
			}
		}
	}
}

func TestSelectedModelMetadata(t *testing.T) {
	m := providers.Model{ID: "ya/model", NativeID: "gpt://folder/model", ProviderID: "ya"}
	h := http.Header{}
	setSelectedModelHeaders(h, "auto/smart", m, true)
	if h.Get("X-OmniBridge-Selected-Model") != m.ID || h.Get("X-OmniBridge-Provider") != "ya" || h.Get("X-OmniBridge-Fallback-Used") != "true" {
		t.Fatalf("metadata headers=%v", h)
	}
	out := normalizeResponseModelJSON([]byte(`{"id":"r1","model":"upstream"}`), m)
	var v map[string]any
	if json.Unmarshal(out, &v) != nil || v["model"] != m.ID || v["omnibridge_native_model"] != m.NativeID {
		t.Fatalf("normalized response=%s", out)
	}
}
