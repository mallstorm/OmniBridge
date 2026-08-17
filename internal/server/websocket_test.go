package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
)

func TestWebSocketCanonicalStreamEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "model-ws"}}})
		case "/v1/chat/completions":
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["stream"] != true {
				t.Errorf("websocket transport must request upstream stream")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"cws\",\"model\":\"model-ws\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello-ws\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"cws\",\"model\":\"model-ws\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:mock", "secret")
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "openai-compatible", BaseURL: upstream.URL + "/v1", APIType: "chat-completions", AuthType: "bearer", CredentialRef: "provider:mock", Enabled: true})
	_ = cfg.UpsertProvider(p)
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	fmt.Fprintf(conn, "GET /v1/ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: omnibridge.stream.v1\r\nAuthorization: Bearer %s\r\n\r\n", u.Host, key, cfg.Get().DataAPIKey)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake=%s", status)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "" {
			break
		}
	}
	req := []byte(`{"type":"request","api_type":"chat-completions","request":{"model":"mock/model-ws","messages":[{"role":"user","content":"hi"}]}}`)
	if err := writeMaskedTestFrame(conn, req); err != nil {
		t.Fatal(err)
	}
	foundText, foundDone := false, false
	for i := 0; i < 10; i++ {
		op, payload, err := readServerTestFrame(br)
		if err != nil {
			t.Fatal(err)
		}
		if op == 0x8 {
			break
		}
		if op != 0x1 {
			continue
		}
		var ev map[string]any
		if json.Unmarshal(payload, &ev) != nil {
			continue
		}
		if ev["type"] == "text_delta" && ev["text_delta"] == "hello-ws" {
			foundText = true
		}
		if ev["type"] == "done" {
			foundDone = true
		}
	}
	if !foundText || !foundDone {
		t.Fatalf("canonical events text=%v done=%v", foundText, foundDone)
	}
	if st.Snapshot().Total.TotalTokens != 3 {
		t.Fatalf("stats tokens=%d", st.Snapshot().Total.TotalTokens)
	}
}

func TestWebSocketRejectsMissingDataKey(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func writeMaskedTestFrame(w io.Writer, p []byte) error {
	mask := []byte{1, 2, 3, 4}
	h := []byte{0x81}
	n := len(p)
	if n < 126 {
		h = append(h, 0x80|byte(n))
	} else {
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	}
	h = append(h, mask...)
	q := append([]byte(nil), p...)
	for i := range q {
		q[i] ^= mask[i%4]
	}
	_, err := w.Write(append(h, q...))
	return err
}
func readServerTestFrame(r *bufio.Reader) (byte, []byte, error) {
	b1, e := r.ReadByte()
	if e != nil {
		return 0, nil, e
	}
	b2, e := r.ReadByte()
	if e != nil {
		return 0, nil, e
	}
	n := uint64(b2 & 0x7f)
	if n == 126 {
		var x uint16
		e = binary.Read(r, binary.BigEndian, &x)
		n = uint64(x)
	} else if n == 127 {
		e = binary.Read(r, binary.BigEndian, &n)
	}
	if e != nil {
		return 0, nil, e
	}
	p := make([]byte, int(n))
	_, e = io.ReadFull(r, p)
	return b1 & 0x0f, p, e
}
