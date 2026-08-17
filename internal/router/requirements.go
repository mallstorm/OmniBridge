package router

import (
	"encoding/json"
	"math"
	"strings"
)

// Requirements is the request-side capability contract inferred before routing.
// It deliberately contains no prompt text and is safe to expose in diagnostics.
type Requirements struct {
	Streaming           bool `json:"streaming,omitempty"`
	SystemPrompt        bool `json:"system_prompt,omitempty"`
	ToolCalling         bool `json:"tool_calling,omitempty"`
	ParallelTools       bool `json:"parallel_tools,omitempty"`
	StructuredOutput    bool `json:"structured_output,omitempty"`
	Vision              bool `json:"vision,omitempty"`
	Reasoning           bool `json:"reasoning,omitempty"`
	AgentMode           bool `json:"agent_mode,omitempty"`
	EstimatedInput      int  `json:"estimated_input_tokens,omitempty"`
	RequestedOutput     int  `json:"requested_output_tokens,omitempty"`
	MinimumContext      int  `json:"minimum_context_tokens,omitempty"`
	RequiresDoctorProof bool `json:"requires_doctor_proof,omitempty"`
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func intValue(v any) int {
	switch x := v.(type) {
	case float64:
		if x > 0 && x < math.MaxInt32 {
			return int(x)
		}
	case int:
		if x > 0 {
			return x
		}
	case json.Number:
		if n, err := x.Int64(); err == nil && n > 0 && n < math.MaxInt32 {
			return int(n)
		}
	}
	return 0
}

func typeName(m map[string]any) string {
	t, _ := m["type"].(string)
	return strings.ToLower(strings.TrimSpace(t))
}

func walkCapability(v any, req *Requirements, state *walkState) {
	switch x := v.(type) {
	case map[string]any:
		t := typeName(x)
		if strings.Contains(t, "image") || t == "input_image" || t == "image_url" {
			req.Vision = true
		}
		if t == "tool_result" || t == "function_call_output" || t == "tool_use" || t == "function_call" {
			state.toolHistory = true
		}
		if role, _ := x["role"].(string); strings.EqualFold(role, "system") || strings.EqualFold(role, "developer") {
			req.SystemPrompt = true
		}
		if role, _ := x["role"].(string); strings.EqualFold(role, "tool") {
			state.toolHistory = true
		}
		if tc, ok := x["tool_calls"].([]any); ok && len(tc) > 0 {
			state.toolHistory = true
		}
		for k, child := range x {
			lk := strings.ToLower(k)
			if lk == "image_url" || lk == "input_image" {
				req.Vision = true
			}
			walkCapability(child, req, state)
		}
	case []any:
		for _, child := range x {
			walkCapability(child, req, state)
		}
	}
}

type walkState struct {
	toolHistory bool
}

func estimateTokens(v any) int {
	chars := 0
	var walk func(any)
	walk = func(x any) {
		switch y := x.(type) {
		case string:
			// Do not count embedded base64 bytes as text tokens. Vision providers account
			// for image tokens separately and the encoded payload would wildly overestimate.
			if strings.HasPrefix(y, "data:image/") && strings.Contains(y, ";base64,") {
				chars += 256
				return
			}
			chars += len([]rune(y))
		case map[string]any:
			for k, z := range y {
				chars += len(k)
				walk(z)
			}
		case []any:
			for _, z := range y {
				walk(z)
			}
		}
	}
	walk(v)
	if chars == 0 {
		return 0
	}
	// Conservative tokenizer-independent approximation for routing preflight.
	return int(math.Ceil(float64(chars) / 3.6))
}

// AnalyzeRequest infers the hard capabilities needed by Chat Completions,
// Responses, Messages, and compatible request bodies.
func AnalyzeRequest(body []byte, apiType string) Requirements {
	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if dec.Decode(&root) != nil {
		return Requirements{}
	}
	req := Requirements{}
	req.Streaming = boolValue(root["stream"])
	if sys, ok := root["system"].(string); ok && strings.TrimSpace(sys) != "" {
		req.SystemPrompt = true
	}
	if ins, ok := root["instructions"].(string); ok && strings.TrimSpace(ins) != "" {
		req.SystemPrompt = true
	}
	if tools, ok := root["tools"].([]any); ok && len(tools) > 0 {
		req.ToolCalling = true
	}
	if boolValue(root["parallel_tool_calls"]) {
		req.ToolCalling = true
		req.ParallelTools = true
	}
	if _, ok := root["response_format"]; ok {
		req.StructuredOutput = true
	}
	if text, ok := root["text"].(map[string]any); ok {
		if format, exists := text["format"]; exists && format != nil {
			req.StructuredOutput = true
		}
	}
	if _, ok := root["reasoning"]; ok {
		req.Reasoning = true
	}
	if v, ok := root["reasoning_effort"].(string); ok && strings.TrimSpace(v) != "" {
		req.Reasoning = true
	}
	if _, ok := root["thinking"]; ok {
		req.Reasoning = true
	}
	state := &walkState{}
	walkCapability(root, &req, state)

	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if n := intValue(root[key]); n > 0 {
			req.RequestedOutput = n
			break
		}
	}
	if req.RequestedOutput <= 0 {
		req.RequestedOutput = 1024
	}
	req.EstimatedInput = estimateTokens(root)
	req.MinimumContext = req.EstimatedInput + req.RequestedOutput

	toolChoiceExplicit := false
	if v, ok := root["tool_choice"].(string); ok {
		v = strings.ToLower(strings.TrimSpace(v))
		toolChoiceExplicit = v != "" && v != "auto" && v != "none"
	} else if root["tool_choice"] != nil {
		toolChoiceExplicit = true
	}
	if prev, ok := root["previous_response_id"].(string); ok && strings.TrimSpace(prev) != "" {
		state.toolHistory = true
	}
	req.AgentMode = req.ToolCalling && (state.toolHistory || toolChoiceExplicit)
	req.RequiresDoctorProof = req.ToolCalling || req.ParallelTools || req.StructuredOutput || req.Vision || req.Reasoning || req.AgentMode
	return req
}

func (r Requirements) HardCapabilityCount() int {
	n := 0
	for _, v := range []bool{r.Streaming, r.SystemPrompt, r.ToolCalling, r.ParallelTools, r.StructuredOutput, r.Vision, r.Reasoning, r.AgentMode} {
		if v {
			n++
		}
	}
	return n
}
