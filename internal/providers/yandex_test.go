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
