package adaptive

import (
	"testing"
	"time"

	"omnibridge/internal/config"
)

func TestColdStartAndConfidence(t *testing.T) {
	e := New(nil, config.AdaptiveConfig{Enabled: true, SuccessAlpha: .2, LatencyAlpha: .1, FullConfidenceAt: 50, QualityWeight: 32, EventQueueSize: 16, DecisionTTLMinutes: 60, MaxDecisions: 20})
	cold := e.Quality("p", "p", "m")
	if cold.Score != .5 || cold.Confidence != 0 {
		t.Fatalf("cold=%+v", cold)
	}
	for i := 0; i < 10; i++ {
		e.apply(Outcome{ProviderID: "p", ConnectionID: "p", ModelID: "m", Success: true, LatencyMS: 100, At: time.Now()})
	}
	q := e.Quality("p", "p", "m")
	if q.Confidence < .19 || q.Confidence > .21 || q.Score <= .5 {
		t.Fatalf("quality=%+v", q)
	}
}
func TestDecisionRetention(t *testing.T) {
	e := New(nil, config.AdaptiveConfig{Enabled: true, EventQueueSize: 16, DecisionTTLMinutes: 60, MaxDecisions: 10})
	e.RecordDecision(Decision{TraceID: "trace", Candidates: []CandidateDecision{{ModelID: "m", Status: "dispatched"}}})
	if _, ok := e.Explain("trace"); !ok {
		t.Fatal("decision missing")
	}
}
