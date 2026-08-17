package router

import "fmt"

type Compatibility struct {
	Compatible bool     `json:"compatible"`
	Reasons    []string `json:"reasons,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// CheckCompatibility performs request/candidate gates. For ordinary explicit
// routes an unprobed capability is treated as unknown (warning) rather than false.
// Auto routes become fail-closed only when the request/profile needs a capability
// that OmniBridge promises to prove with Model Doctor.
func CheckCompatibility(c Candidate, r Requirements, requireDoctorProof bool) Compatibility {
	out := Compatibility{Compatible: true}
	reject := func(reason string) {
		out.Compatible = false
		out.Reasons = append(out.Reasons, reason)
	}
	strictEvidence := requireDoctorProof && r.RequiresDoctorProof
	if strictEvidence && !c.DoctorVerified {
		reject("doctor_verification_required")
	}
	check := func(required, supported bool, reason string) {
		if !required || supported {
			return
		}
		if c.DoctorVerified || strictEvidence {
			reject(reason)
			return
		}
		out.Warnings = append(out.Warnings, "unverified_"+reason)
	}
	check(r.Streaming, c.Streaming, "streaming_not_supported")
	check(r.SystemPrompt, c.SystemPrompt, "system_prompt_not_supported")
	check(r.ToolCalling, c.ToolCalling, "tool_calling_not_supported")
	check(r.ParallelTools, c.ParallelTools, "parallel_tools_not_supported")
	check(r.StructuredOutput, c.StructuredOutput, "structured_output_not_supported")
	check(r.Vision, c.Vision, "vision_not_supported")
	check(r.Reasoning, c.Reasoning, "reasoning_not_supported")
	check(r.AgentMode, c.AgentReady, "agent_mode_not_supported")
	if r.MinimumContext > 0 && c.ContextWindow > 0 && r.MinimumContext > c.ContextWindow {
		reject(fmt.Sprintf("context_window_too_small:%d<%d", c.ContextWindow, r.MinimumContext))
	}
	if r.RequestedOutput > 0 && c.MaxOutputTokens > 0 && r.RequestedOutput > c.MaxOutputTokens {
		reject(fmt.Sprintf("max_output_too_small:%d<%d", c.MaxOutputTokens, r.RequestedOutput))
	}
	if r.MinimumContext > 0 && c.ContextWindow <= 0 {
		out.Warnings = append(out.Warnings, "context_window_unknown")
	}
	if !strictEvidence && r.RequiresDoctorProof && !c.DoctorVerified {
		out.Warnings = append(out.Warnings, "capabilities_not_doctor_verified")
	}
	return out
}
