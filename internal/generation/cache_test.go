package generation

import (
	"testing"
	"time"

	"omnibridge/internal/config"
)

func TestCacheabilityProtectsToolsStreamingAndImplicitTemperature(t *testing.T) {
	if !Cacheable([]byte(`{"temperature":0,"stream":false,"messages":[]}`)) {
		t.Fatal("explicit deterministic request should be cacheable")
	}
	for _, raw := range []string{
		`{"messages":[]}`,
		`{"temperature":null}`,
		`{"temperature":0.1}`,
		`{"temperature":0,"stream":true}`,
		`{"temperature":0,"tools":[{"type":"function"}]}`,
	} {
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

func TestLRUEvictionTouchesHits(t *testing.T) {
	m := New(config.ExactCacheConfig{Enabled: true, TTLSeconds: 60, MaxEntries: 2, MaxEntryBytes: 4096})
	for _, key := range []string{"a", "b"} {
		if !m.Put(Entry{Signature: key, Body: []byte(key)}) {
			t.Fatalf("put %s failed", key)
		}
	}
	if _, ok := m.Get("a"); !ok {
		t.Fatal("expected cache hit for a")
	}
	m.Put(Entry{Signature: "c", Body: []byte("c")})
	if _, ok := m.Get("b"); ok {
		t.Fatal("least-recently-used entry b was not evicted")
	}
	if _, ok := m.Get("a"); !ok {
		t.Fatal("recently touched entry a was evicted")
	}
}

func TestExpiredDedupResultsArePruned(t *testing.T) {
	m := New(config.ExactCacheConfig{Deduplicate: true})
	m.mu.Lock()
	m.completed["expired"] = Entry{Signature: "expired", ExpiresAt: time.Now().Add(-time.Second), Body: []byte("old")}
	m.mu.Unlock()
	m.Snapshot()
	m.mu.Lock()
	_, ok := m.completed["expired"]
	m.mu.Unlock()
	if ok {
		t.Fatal("expired dedup result was retained")
	}
}
