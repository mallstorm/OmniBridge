package generation

import (
	"omnibridge/internal/config"
	"testing"
	"time"
)

func TestCacheabilityProtectsToolsAndStreaming(t *testing.T) {
	if !Cacheable([]byte(`{"temperature":0,"stream":false,"messages":[]}`)) {
		t.Fatal("deterministic request should be cacheable")
	}
	for _, raw := range []string{`{"temperature":0.1}`, `{"temperature":0,"stream":true}`, `{"temperature":0,"tools":[{"type":"function"}]}`} {
		if Cacheable([]byte(raw)) {
			t.Fatalf("must not cache %s", raw)
		}
	}
}
func TestPhysicalIdentityAndNamespaceIsolateCache(t *testing.T) {
	cfg := config.ExactCacheConfig{Enabled: true, TTLSeconds: 60, MaxEntries: 10, MaxEntryBytes: 4096, Deduplicate: true}
	m := New(cfg)
	raw := []byte(`{"temperature":0}`)
	a := Signature(Namespace("client-a"), "p", "m", "native", IdentityVersion("m", "native", time.Unix(1, 0)), raw)
	b := Signature(Namespace("client-b"), "p", "m", "native", IdentityVersion("m", "native", time.Unix(1, 0)), raw)
	if a == b {
		t.Fatal("client namespaces collided")
	}
	m.Put(Entry{Signature: a, Body: []byte("ok")})
	if _, ok := m.Get(b); ok {
		t.Fatal("cross-client cache hit")
	}
}
