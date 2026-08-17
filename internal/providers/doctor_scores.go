package providers

import "math"

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return math.Round(v*10) / 10
}

// ApplyDoctorScores derives stable 0..100 scores from a deep Model Doctor result.
// Scores describe proven compatibility, not model intelligence. Runtime reliability
// remains a separate Router signal and is combined later with this probe score.
func ApplyDoctorScores(r *ProbeResult) {
	if r == nil {
		return
	}
	doctor := 0.0
	if r.Basic {
		doctor += 15
	}
	if r.Streaming {
		doctor += 10
	}
	if r.SystemPrompt {
		doctor += 10
	}
	if r.ToolCalling {
		doctor += 15
	}
	if r.ToolRoundTrip {
		doctor += 15
	}
	if r.ParallelTools {
		doctor += 10
	}
	if r.StructuredOutput {
		doctor += 10
	}
	if r.Vision {
		doctor += 5
	}
	if r.Reasoning {
		doctor += 5
	}
	if r.AgentCompatible {
		doctor += 5
	}

	agent := 0.0
	if r.Basic {
		agent += 10
	}
	if r.Streaming {
		agent += 15
	}
	if r.SystemPrompt {
		agent += 10
	}
	if r.ToolCalling {
		agent += 20
	}
	if r.ToolRoundTrip {
		agent += 20
	}
	if r.ParallelTools {
		agent += 10
	}
	if r.StructuredOutput {
		agent += 5
	}
	if r.AgentCompatible {
		agent += 10
	}

	coding := 0.0
	if r.Basic {
		coding += 15
	}
	if r.Streaming {
		coding += 15
	}
	if r.SystemPrompt {
		coding += 10
	}
	if r.ToolCalling {
		coding += 20
	}
	if r.ToolRoundTrip {
		coding += 20
	}
	if r.StructuredOutput {
		coding += 10
	}
	if r.Reasoning {
		coding += 5
	}
	if r.AgentCompatible {
		coding += 5
	}

	protocols := 0
	passed := 0
	for _, pr := range r.Protocols {
		protocols++
		if pr.Basic {
			passed++
		}
	}
	reliability := 0.0
	if protocols > 0 {
		reliability = float64(passed) / float64(protocols) * 70
	}
	if r.Basic {
		reliability += 15
	}
	if r.Streaming {
		reliability += 5
	}
	if r.ToolRoundTrip {
		reliability += 5
	}
	if r.AgentCompatible {
		reliability += 5
	}

	r.DoctorScore = clampScore(doctor)
	r.AgentScore = clampScore(agent)
	r.CodingScore = clampScore(coding)
	r.ReliabilityScore = clampScore(reliability)
}
