package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"testing"
)

func TestPrepareYandexProviderRequiresFolderID(t *testing.T) {
	_, err := prepareYandexProvider(config.Provider{ID: "ya", Kind: "yandex"})
	if err == nil {
		t.Fatal("expected missing Folder ID to be rejected")
	}
	p, err := prepareYandexProvider(config.Provider{ID: "ya", Kind: "yandex", ProjectID: "b1g-folder"})
	if err != nil {
		t.Fatal(err)
	}
	if p.AuthType != "yandex-api-key" || p.Headers["OpenAI-Project"] != "b1g-folder" {
		t.Fatalf("unexpected normalized provider: %+v", p)
	}
}

func TestYandexModelMetaAndFriendlyName(t *testing.T) {
	cases := []struct {
		slug, wantName string
		wantCtx        int
		wantTool       bool
	}{
		{"yandexgpt/latest", "YandexGPT", 32768, true},
		{"yandexgpt-5.1", "YandexGPT 5.1", 32768, true},
		{"deepseek-v4-flash/latest", "DeepSeek V4 Flash", 1048576, true},
		{"qwen3-235b-a22b-fp8/latest", "Qwen3 235B A22B FP8", 262144, true},
		{"gpt-oss-120b/latest", "GPT-OSS 120B", 131072, true},
	}
	for _, c := range cases {
		spec, ok := yandexSpecFor(c.slug)
		if !ok {
			t.Fatalf("slug %q has no spec", c.slug)
		}
		if spec.Display != c.wantName || spec.ContextWindow != c.wantCtx || spec.ToolCalling != c.wantTool {
			t.Fatalf("slug %q spec = %+v", c.slug, spec)
		}
		if got := yandexFriendlyName(c.slug); got != c.wantName {
			t.Fatalf("friendly name %q != %q", got, c.wantName)
		}
	}
	if got := yandexFriendlyName("custom-model-v1"); got == "" || got == "custom-model-v1" {
		t.Fatalf("unknown slug fallback unexpected: %q", got)
	}
}

func TestYandexModelURINormalization(t *testing.T) {
	if got := yandexModelURI("b1g-folder", "yandexgpt/latest"); got != "gpt://b1g-folder/yandexgpt/latest" {
		t.Fatalf("short id not prefixed: %q", got)
	}
	if got := yandexModelURI("b1g-folder", "gpt://b1g-folder/yandexgpt/latest"); got != "gpt://b1g-folder/yandexgpt/latest" {
		t.Fatalf("foldered uri changed: %q", got)
	}
	if got := yandexModelURI("b1g-folder", ""); got != "" {
		t.Fatalf("empty id: %q", got)
	}
}

func TestYandexChatUsableGate(t *testing.T) {
	chat := []string{
		"gpt://b1g-folder/yandexgpt/latest",
		"gpt://b1g-folder/yandexgpt-5.1",
		"gpt://b1g-folder/yandexgpt-5-pro/latest",
		"gpt://b1g-folder/yandexgpt-5-lite",
		"gpt://b1g-folder/deepseek-v4-flash/latest",
		"yandexgpt-5-pro",
	}
	for _, id := range chat {
		if !YandexChatUsable(id) {
			t.Fatalf("expected chat model %q to be usable", id)
		}
	}
	nonChat := []string{
		"gpt://b1g-folder/speech-realtime-deepseek-v4-flash",
		"gpt://b1g-folder/speech-realtime-yandexgpt",
		"gpt://b1g-folder/tts-markov",
		"gpt://b1g-folder/text-to-image/xl",
		"gpt://b1g-folder/embeddings/v1",
	}
	for _, id := range nonChat {
		if YandexChatUsable(id) {
			t.Fatalf("expected non-chat model %q to be excluded", id)
		}
	}
}

func TestYandexListModelsFriendlyNamesAndURIs(t *testing.T) {
	var gotProject string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = r.Header.Get("OpenAI-Project")
		w.Header().Set("Content-Type", "application/json")
		// Discovery returns short ids (no gpt:// prefix) to prove normalization.
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"yandexgpt/latest","owned_by":"yandex"},{"id":"gpt://b1g-folder/deepseek-v4-flash/latest","owned_by":"yandex"},{"id":"embedding","owned_by":"yandex"}]}`)
	}))
	defer upstream.Close()

	secrets := secretstore.New(filepath.Join(t.TempDir(), "secrets.json"))
	if err := secrets.Put("provider:ya", "AQVN-test-key"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{
		ID: "ya", Name: "Yandex", Kind: "yandex", ProjectID: "b1g-folder",
		BaseURL: upstream.URL, ModelsURL: upstream.URL, CredentialRef: "provider:ya", Enabled: true,
		PreferredProtocol: "responses", Protocols: []string{"responses", "chat-completions"},
	}
	models, err := m.ListModels(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if gotProject != "b1g-folder" {
		t.Fatalf("OpenAI-Project header missing: %q", gotProject)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 generation models, got %d: %+v", len(models), models)
	}
	byNative := map[string]Model{}
	for _, m := range models {
		byNative[m.NativeID] = m
	}
	gpt := byNative["gpt://b1g-folder/yandexgpt/latest"]
	if gpt.ID != "ya/gpt://b1g-folder/yandexgpt/latest" || gpt.Name != "YandexGPT" || !gpt.Streaming || !gpt.ToolCalling {
		t.Fatalf("yandexgpt model metadata wrong: %+v", gpt)
	}
	if gpt.ContextWindow != 32768 || gpt.MaxOutputTokens != 8192 {
		t.Fatalf("yandexgpt context/metadata wrong: %+v", gpt)
	}
	deep := byNative["gpt://b1g-folder/deepseek-v4-flash/latest"]
	if deep.Name != "DeepSeek V4 Flash" || !deep.Reasoning || deep.ContextWindow != 1048576 {
		t.Fatalf("deepseek model metadata wrong: %+v", deep)
	}
	for _, m := range models {
		if m.ProviderID != "ya" || len(m.Protocols) != 2 {
			t.Fatalf("model defaults wrong: %+v", m)
		}
	}
}

func TestYandexCuratedFallback(t *testing.T) {
	models := yandexCuratedModels("b1g-folder")
	if len(models) == 0 {
		t.Fatal("curated fallback is empty")
	}
	for _, m := range models {
		if m.NativeID == "" || !m.Streaming || m.Name == "" {
			t.Fatalf("curated model incomplete: %+v", m)
		}
	}
}

func TestYandexListModelsUsesApiKeyAndProjectHeaders(t *testing.T) {
	var gotAuthorization, gotProject string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotProject = r.Header.Get("OpenAI-Project")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gpt://b1g-folder/yandexgpt-5.1","owned_by":"yandex"}]}`)
	}))
	defer upstream.Close()

	secrets := secretstore.New(filepath.Join(t.TempDir(), "secrets.json"))
	if err := secrets.Put("provider:ya", "AQVN-test-key"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{
		ID: "ya", Name: "Yandex", Kind: "yandex", ProjectID: "b1g-folder",
		BaseURL: upstream.URL, ModelsURL: upstream.URL, CredentialRef: "provider:ya", Enabled: true,
	}
	models, err := m.ListModels(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Api-Key AQVN-test-key" || gotProject != "b1g-folder" {
		t.Fatalf("bad headers authorization=%q project=%q", gotAuthorization, gotProject)
	}
	if len(models) != 1 || models[0].NativeID != "gpt://b1g-folder/yandexgpt-5.1" {
		t.Fatalf("unexpected models: %+v", models)
	}
}
