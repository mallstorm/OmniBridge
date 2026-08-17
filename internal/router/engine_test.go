package router

import (
	"testing"
	"time"
)

func cs() []Candidate {
	return []Candidate{
		{ID: "a", Requests24h: 100, AverageLatencyMS: 900, SuccessRate: 99, QuotaKnown: true, QuotaRemaining: 20, CostKnown: true, CostPerMTokens: 10, AgentReady: true, ToolCalling: true, Streaming: true},
		{ID: "b", Requests24h: 5, AverageLatencyMS: 300, SuccessRate: 97, QuotaKnown: true, QuotaRemaining: 90, CostKnown: true, CostPerMTokens: 1},
		{ID: "c", Requests24h: 20, AverageLatencyMS: 500, SuccessRate: 99},
	}
}

func TestStrategies(t *testing.T) {
	e := New()
	if got := e.Order(cs(), Options{Mode: "fastest"}); got[0].ID != "b" {
		t.Fatalf("fastest=%s", got[0].ID)
	}
	if got := e.Order(cs(), Options{Mode: "least_used"}); got[0].ID != "b" {
		t.Fatalf("least=%s", got[0].ID)
	}
	if got := e.Order(cs(), Options{Mode: "quota_optimized"}); got[0].ID != "b" {
		t.Fatalf("quota=%s", got[0].ID)
	}
	if got := e.Order(cs(), Options{Mode: "cost_optimized"}); got[0].ID != "b" {
		t.Fatalf("cost=%s", got[0].ID)
	}
	r1 := e.Order(cs(), Options{Mode: "round_robin"})[0].ID
	r2 := e.Order(cs(), Options{Mode: "round_robin"})[0].ID
	if r1 == r2 {
		t.Fatalf("round robin did not rotate: %s", r1)
	}
}

func TestWeightedDistribution(t *testing.T) {
	e := New()
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		counts[e.Order(cs()[:2], Options{Mode: "weighted", Weights: map[string]float64{"a": 3, "b": 1}})[0].ID]++
	}
	if counts["a"] != 30 || counts["b"] != 10 {
		t.Fatalf("smooth weights expected 30/10, got %+v", counts)
	}
}

func TestAffinity(t *testing.T) {
	e := New()
	in := cs()
	e.Pin("auto/coding", "session", "a", time.Minute)
	got := e.Order(in, Options{Mode: "fastest", RouteKey: "auto/coding", SessionKey: "session", SessionStickiness: true})
	if got[0].ID != "a" {
		t.Fatalf("pin not honored: %s", got[0].ID)
	}
	in[0].HealthRank = 1
	got = e.Order(in, Options{Mode: "fastest", RouteKey: "auto/coding", SessionKey: "session", SessionStickiness: true})
	if got[0].ID == "a" {
		t.Fatalf("degraded pin should not override routing")
	}
	e.Release("auto/coding", "session", "a")
	if e.Snapshot().AffinityBindings != 0 {
		t.Fatal("release failed")
	}
}

func TestAutoCodingPrefersCapableModel(t *testing.T) {
	e := New()
	in := []Candidate{
		{ID: "fast", AverageLatencyMS: 100, SuccessRate: 99, Requests24h: 10},
		{ID: "agent", AverageLatencyMS: 800, SuccessRate: 99, Requests24h: 10, AgentReady: true, ToolCalling: true, Streaming: true},
	}
	got := e.Order(in, Options{Mode: "auto", Profile: "coding"})
	if got[0].ID != "agent" {
		t.Fatalf("auto/coding=%s", got[0].ID)
	}
}
