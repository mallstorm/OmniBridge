package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func generatedToolID(messageIndex, toolIndex int, name, args string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", messageIndex, toolIndex, name, args)))
	return "call_ob_" + hex.EncodeToString(h[:6])
}

func stringJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func coerceSchema(v any) (any, bool) {
	if v == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{"type": "object", "properties": map[string]any{}}, true
	}
	changed := false
	out := make(map[string]any, len(m)+2)
	for k, val := range m {
		switch k {
		case "properties":
			props, ok := val.(map[string]any)
			if !ok {
				out[k] = map[string]any{}
				changed = true
				continue
			}
			clean := make(map[string]any, len(props))
			for pk, pv := range props {
				if child, c := coerceNestedSchema(pv); c {
					clean[pk] = child
					changed = true
				} else {
					clean[pk] = child
				}
			}
			out[k] = clean
		case "required":
			arr, ok := val.([]any)
			if !ok {
				if ss, ok := val.([]string); ok {
					x := make([]any, 0, len(ss))
					for _, s := range ss {
						x = append(x, s)
					}
					out[k] = x
				} else {
					changed = true
				}
				continue
			}
			out[k] = arr
		default:
			child, c := coerceNestedSchema(val)
			out[k] = child
			changed = changed || c
		}
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
		changed = true
	}
	if strings.EqualFold(fmt.Sprint(out["type"]), "object") {
		if _, ok := out["properties"]; !ok {
			out["properties"] = map[string]any{}
			changed = true
		}
	}
	return out, changed
}

func coerceNestedSchema(v any) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		changed := false
		for k, val := range x {
			child, c := coerceNestedSchema(val)
			out[k] = child
			changed = changed || c
		}
		if props, ok := out["properties"]; ok {
			if _, ok := props.(map[string]any); !ok {
				out["properties"] = map[string]any{}
				changed = true
			}
			if _, ok := out["type"]; !ok {
				out["type"] = "object"
				changed = true
			}
		}
		return out, changed
	case []any:
		out := make([]any, len(x))
		changed := false
		for i, val := range x {
			out[i], changed = func(prev bool) (any, bool) {
				child, c := coerceNestedSchema(val)
				return child, prev || c
			}(changed)
		}
		return out, changed
	default:
		return v, false
	}
}

func normalizeReasoning(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "off", "disabled", "false":
			return "none", s != "none"
		case "maximum", "max":
			return "high", true
		default:
			return x, false
		}
	case map[string]any:
		out := make(map[string]any, len(x))
		changed := false
		for k, val := range x {
			out[k] = val
		}
		if effort, ok := out["effort"].(string); ok {
			n, c := normalizeReasoning(effort)
			out["effort"] = n
			changed = changed || c
		}
		if _, ok := out["effort"]; !ok {
			if effort, ok := out["reasoning_effort"].(string); ok {
				out["effort"] = effort
				delete(out, "reasoning_effort")
				changed = true
			}
		}
		return out, changed
	default:
		return v, false
	}
}

// NormalizeCanonicalRequest repairs protocol-invalid agent histories only when
// the repair is deterministic. It keeps ordinary user/assistant text intact.
func NormalizeCanonicalRequest(req Request) (Request, NormalizationReport) {
	report := NormalizationReport{}
	if n, changed := normalizeReasoning(req.Reasoning); changed {
		req.Reasoning = n
		report.ReasoningNormalized++
	}
	for i := range req.Tools {
		if schema, changed := coerceSchema(req.Tools[i].Parameters); changed {
			req.Tools[i].Parameters = schema
			report.SchemasCoerced++
		}
	}
	// Hoist canonical system/developer messages into the dedicated system field.
	base := make([]Message, 0, len(req.Messages)+4)
	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" || role == "developer" {
			if t := partsText(msg.Parts); t != "" {
				if req.System != "" {
					req.System += "\n"
				}
				req.System += t
			}
			report.SystemMessagesHoisted++
			continue
		}
		msg.Role = role
		base = append(base, msg)
	}
	req.Messages = base

	// Ensure every tool call has a stable ID and valid JSON arguments when a
	// conservative repair is possible.
	for mi := range req.Messages {
		for ti := range req.Messages[mi].ToolCalls {
			tc := &req.Messages[mi].ToolCalls[ti]
			if strings.TrimSpace(tc.ID) == "" {
				tc.ID = generatedToolID(mi, ti, tc.Name, tc.Arguments)
				report.ToolIDsGenerated++
			}
			before := tc.Arguments
			tc.Arguments = repairArguments(tc.Arguments)
			if tc.Arguments != before {
				report.ArgumentsRepaired++
			}
		}
	}

	// Repair missing tool results and neutralize orphan tool-result messages.
	out := make([]Message, 0, len(req.Messages)+4)
	pending := map[string]ToolCall{}
	flushPending := func() {
		if len(pending) == 0 {
			return
		}
		// Preserve deterministic order by walking prior assistant calls.
		for _, prior := range out {
			for _, tc := range prior.ToolCalls {
				if _, ok := pending[tc.ID]; ok {
					out = append(out, Message{Role: "tool", ToolCallID: tc.ID, Parts: []Part{{Type: "text", Text: ""}}})
					delete(pending, tc.ID)
					report.MissingToolResults++
				}
			}
		}
	}
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" && len(pending) == 1 {
				for k := range pending {
					id = k
				}
				msg.ToolCallID = id
			}
			if _, ok := pending[id]; ok && id != "" {
				delete(pending, id)
				out = append(out, msg)
				continue
			}
			report.OrphanToolResults++
			report.Warnings = append(report.Warnings, "orphan tool result converted to user content")
			label := "[orphan tool result"
			if id != "" {
				label += " " + id
			}
			label += "] "
			out = append(out, Message{Role: "user", Parts: []Part{{Type: "text", Text: label + partsText(msg.Parts)}}})
			continue
		}
		if len(pending) > 0 {
			flushPending()
		}
		out = append(out, msg)
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				pending[tc.ID] = tc
			}
		}
	}
	flushPending()
	req.Messages = out
	return req, report
}

func NormalizeCanonicalResponse(res Response) (Response, NormalizationReport) {
	report := NormalizationReport{}
	for i := range res.ToolCalls {
		tc := &res.ToolCalls[i]
		if strings.TrimSpace(tc.ID) == "" {
			tc.ID = generatedToolID(0, i, tc.Name, tc.Arguments)
			report.ToolIDsGenerated++
		}
		before := tc.Arguments
		tc.Arguments = repairArguments(tc.Arguments)
		if before != tc.Arguments {
			report.ArgumentsRepaired++
		}
	}
	if len(res.ToolCalls) > 0 && strings.TrimSpace(res.FinishReason) == "" {
		res.FinishReason = "tool_calls"
	}
	if res.TotalTokens == 0 {
		res.TotalTokens = res.InputTokens + res.OutputTokens
	}
	return res, report
}

// NormalizeSameProtocolRequest performs a lossless top-level normalization pass:
// unknown client/provider fields are preserved while agent-critical structures
// receive safe syntactic repairs.
func NormalizeSameProtocolRequest(apiType string, body []byte) ([]byte, NormalizationReport, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, NormalizationReport{}, err
	}
	report := NormalizationReport{UnknownFieldsPreserved: true}
	for k := range root {
		if strings.HasPrefix(strings.ToLower(k), "_omnibridge_") {
			delete(root, k)
		}
	}
	if reasoning, ok := root["reasoning"]; ok {
		if n, changed := normalizeReasoning(reasoning); changed {
			root["reasoning"] = n
			report.ReasoningNormalized++
		}
	}
	if effort, ok := root["reasoning_effort"]; ok {
		if n, changed := normalizeReasoning(effort); changed {
			root["reasoning_effort"] = n
			report.ReasoningNormalized++
		}
	}
	coerceRawTools(apiType, root, &report)
	normalizeRawAgentHistory(apiType, root, &report)
	out, err := json.Marshal(root)
	return out, report, err
}

func NormalizeSameProtocolResponse(apiType string, body []byte) ([]byte, NormalizationReport, error) {
	var root map[string]any
	report := NormalizationReport{UnknownFieldsPreserved: true}
	if err := json.Unmarshal(body, &root); err != nil {
		normalized := NormalizeAgentJSON(body)
		if err2 := json.Unmarshal(normalized, &root); err2 != nil {
			return body, NormalizationReport{}, err
		}
		report.ArgumentsRepaired++ // full-document conservative agent JSON repair was required
	}
	ensureRawResponseToolIDs(apiType, root, &report)
	out, err := json.Marshal(root)
	return out, report, err
}

func coerceRawTools(apiType string, root map[string]any, report *NormalizationReport) {
	arr, _ := root["tools"].([]any)
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		var fn map[string]any
		if apiType == ChatCompletions {
			fn, _ = m["function"].(map[string]any)
		} else {
			fn = m
		}
		if fn == nil {
			continue
		}
		key := "parameters"
		if apiType == Messages {
			key = "input_schema"
		}
		schema, changed := coerceSchema(fn[key])
		if changed {
			fn[key] = schema
			report.SchemasCoerced++
		}
	}
}

func normalizeRawAgentHistory(apiType string, root map[string]any, report *NormalizationReport) {
	switch apiType {
	case ChatCompletions:
		arr, _ := root["messages"].([]any)
		if arr == nil {
			return
		}
		// Hoist misplaced system/developer messages while preserving every field.
		prefix, rest := []any{}, []any{}
		seenNonSystem := false
		for _, raw := range arr {
			m, _ := raw.(map[string]any)
			role := ""
			if m != nil {
				role, _ = m["role"].(string)
			}
			if role == "system" || role == "developer" {
				prefix = append(prefix, raw)
				if seenNonSystem {
					report.SystemMessagesHoisted++
				}
			} else {
				seenNonSystem = true
				rest = append(rest, raw)
			}
		}
		arr = append(prefix, rest...)
		pending := map[string]bool{}
		pendingOrder := []string{}
		out := make([]any, 0, len(arr)+4)
		flush := func() {
			for _, id := range pendingOrder {
				if !pending[id] {
					continue
				}
				out = append(out, map[string]any{"role": "tool", "tool_call_id": id, "content": ""})
				delete(pending, id)
				report.MissingToolResults++
			}
			pendingOrder = pendingOrder[:0]
		}
		for mi, raw := range arr {
			m, _ := raw.(map[string]any)
			if m == nil {
				out = append(out, raw)
				continue
			}
			role, _ := m["role"].(string)
			if role == "tool" {
				id, _ := m["tool_call_id"].(string)
				if id == "" && len(pending) == 1 {
					for k := range pending {
						id = k
					}
					m["tool_call_id"] = id
				}
				if pending[id] {
					delete(pending, id)
					out = append(out, m)
					continue
				}
				report.OrphanToolResults++
				report.Warnings = append(report.Warnings, "orphan tool result converted to user content")
				m["role"] = "user"
				delete(m, "tool_call_id")
				out = append(out, m)
				continue
			}
			if len(pending) > 0 {
				flush()
			}
			if tcs, ok := m["tool_calls"].([]any); ok {
				for ti, tr := range tcs {
					tc, _ := tr.(map[string]any)
					if tc == nil {
						continue
					}
					id, _ := tc["id"].(string)
					fn, _ := tc["function"].(map[string]any)
					name, args := "", ""
					if fn != nil {
						name, _ = fn["name"].(string)
						args, _ = fn["arguments"].(string)
						fixed := repairArguments(args)
						if fixed != args {
							fn["arguments"] = fixed
							report.ArgumentsRepaired++
							args = fixed
						}
					}
					if strings.TrimSpace(id) == "" {
						id = generatedToolID(mi, ti, name, args)
						tc["id"] = id
						report.ToolIDsGenerated++
					}
					if !pending[id] {
						pendingOrder = append(pendingOrder, id)
					}
					pending[id] = true
				}
			}
			out = append(out, m)
		}
		if len(pending) > 0 {
			flush()
		}
		root["messages"] = out
	case Messages:
		arr, _ := root["messages"].([]any)
		for mi, raw := range arr {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			blocks, _ := m["content"].([]any)
			for bi, br := range blocks {
				b, _ := br.(map[string]any)
				if b == nil {
					continue
				}
				if textValue(b["type"]) == "tool_use" && strings.TrimSpace(textValue(b["id"])) == "" {
					b["id"] = generatedToolID(mi, bi, textValue(b["name"]), stringJSON(b["input"]))
					report.ToolIDsGenerated++
				}
			}
		}
	case Responses:
		arr, _ := root["input"].([]any)
		for i, raw := range arr {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			if textValue(m["type"]) == "function_call" {
				id := firstNonEmpty(textValue(m["call_id"]), textValue(m["id"]))
				args := textValue(m["arguments"])
				fixed := repairArguments(args)
				if fixed != args {
					m["arguments"] = fixed
					report.ArgumentsRepaired++
				}
				if id == "" {
					id = generatedToolID(0, i, textValue(m["name"]), fixed)
					m["call_id"] = id
					report.ToolIDsGenerated++
				}
			}
		}
	}
}

func ensureRawResponseToolIDs(apiType string, root map[string]any, report *NormalizationReport) {
	switch apiType {
	case ChatCompletions:
		choices, _ := root["choices"].([]any)
		for ci, raw := range choices {
			c, _ := raw.(map[string]any)
			if c == nil {
				continue
			}
			msg, _ := c["message"].(map[string]any)
			if msg == nil {
				continue
			}
			tcs, _ := msg["tool_calls"].([]any)
			for ti, tr := range tcs {
				tc, _ := tr.(map[string]any)
				if tc == nil {
					continue
				}
				fn, _ := tc["function"].(map[string]any)
				name, args := "", ""
				if fn != nil {
					name, _ = fn["name"].(string)
					args, _ = fn["arguments"].(string)
					fixed := repairArguments(args)
					if fixed != args {
						fn["arguments"] = fixed
						report.ArgumentsRepaired++
						args = fixed
					}
				}
				if strings.TrimSpace(textValue(tc["id"])) == "" {
					tc["id"] = generatedToolID(ci, ti, name, args)
					report.ToolIDsGenerated++
				}
			}
		}
	case Messages:
		blocks, _ := root["content"].([]any)
		for i, raw := range blocks {
			b, _ := raw.(map[string]any)
			if b != nil && textValue(b["type"]) == "tool_use" && strings.TrimSpace(textValue(b["id"])) == "" {
				b["id"] = generatedToolID(0, i, textValue(b["name"]), stringJSON(b["input"]))
				report.ToolIDsGenerated++
			}
		}
	case Responses:
		out, _ := root["output"].([]any)
		for i, raw := range out {
			m, _ := raw.(map[string]any)
			if m != nil && textValue(m["type"]) == "function_call" {
				id := firstNonEmpty(textValue(m["call_id"]), textValue(m["id"]))
				if id == "" {
					id = generatedToolID(0, i, textValue(m["name"]), textValue(m["arguments"]))
					m["call_id"] = id
					report.ToolIDsGenerated++
				}
				if a, ok := m["arguments"].(string); ok {
					fixed := repairArguments(a)
					if fixed != a {
						m["arguments"] = fixed
						report.ArgumentsRepaired++
					}
				}
			}
		}
	}
}
