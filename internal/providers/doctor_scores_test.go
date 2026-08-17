package providers

import "testing"

func TestApplyDoctorScoresFullAgentProbe(t *testing.T) {
	r := ProbeResult{Deep: true, Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, ParallelTools: true, StructuredOutput: true, Vision: true, Reasoning: true, AgentCompatible: true, Protocols: map[string]ProtocolProbe{"chat-completions": {Basic: true}, "responses": {Basic: true}}}
	ApplyDoctorScores(&r)
	if r.DoctorScore != 100 || r.AgentScore != 100 || r.CodingScore != 100 || r.ReliabilityScore != 100 {
		t.Fatalf("unexpected full scores: doctor=%v agent=%v coding=%v reliability=%v", r.DoctorScore, r.AgentScore, r.CodingScore, r.ReliabilityScore)
	}
}

func TestApplyDoctorScoresPartialProbe(t *testing.T) {
	r := ProbeResult{Basic: true, Streaming: true, Protocols: map[string]ProtocolProbe{"chat-completions": {Basic: true}, "responses": {Basic: false}}}
	ApplyDoctorScores(&r)
	if r.DoctorScore <= 0 || r.DoctorScore >= 100 || r.AgentScore >= 100 || r.ReliabilityScore >= 100 {
		t.Fatalf("partial scores should be bounded below full: %+v", r)
	}
}
