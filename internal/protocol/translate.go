package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	ChatCompletions = "chat-completions"
	Messages        = "messages"
	Responses       = "responses"
)

func TranslateRequest(from, to string, body []byte) ([]byte, bool, error) {
	out, stream, _, err := TranslateRequestDetailed(from, to, body)
	return out, stream, err
}

func TranslateRequestDetailed(from, to string, body []byte) ([]byte, bool, NormalizationReport, error) {
	if !CanTranslate(from, to) {
		return nil, false, NormalizationReport{}, fmt.Errorf("protocol translation %s -> %s is not supported", from, to)
	}
	if from == to {
		out, report, err := NormalizeSameProtocolRequest(from, body)
		if err != nil {
			return nil, false, report, err
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			return nil, false, report, err
		}
		stream, _ := m["stream"].(bool)
		return out, stream, report, nil
	}
	req, err := ParseRequest(from, body)
	if err != nil {
		return nil, false, NormalizationReport{}, err
	}
	req, report := NormalizeCanonicalRequest(req)
	originalStream := req.Stream
	// Protocol Engine 3.1 preserves the caller's streaming intent across protocol
	// translation. The server now decodes the native upstream SSE incrementally
	// into CanonicalStreamEvent and immediately encodes the requested downstream
	// dialect instead of buffering the complete response.
	out, err := EncodeRequest(to, req)
	return out, originalStream, report, err
}

func TranslateResponse(from, to string, body []byte) ([]byte, error) {
	out, _, err := TranslateResponseDetailed(from, to, body)
	return out, err
}

func TranslateResponseDetailed(from, to string, body []byte) ([]byte, NormalizationReport, error) {
	if !CanTranslate(from, to) {
		return nil, NormalizationReport{}, fmt.Errorf("protocol translation %s -> %s is not supported", from, to)
	}
	if from == to {
		return NormalizeSameProtocolResponse(from, body)
	}
	res, err := ParseResponse(from, body)
	if err != nil {
		return nil, NormalizationReport{}, err
	}
	res, report := NormalizeCanonicalResponse(res)
	out, err := EncodeResponse(to, res)
	return out, report, err
}

func parseRequestBuiltin(apiType string, body []byte) (Request, error) {
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return Request{}, err
	}
	var r Request
	r.Model, _ = v["model"].(string)
	r.Stream, _ = v["stream"].(bool)
	r.Temperature = v["temperature"]
	r.Reasoning = v["reasoning"]
	if r.Reasoning == nil {
		r.Reasoning = v["reasoning_effort"]
	}
	if b, ok := v["parallel_tool_calls"].(bool); ok {
		r.ParallelTools = &b
	}
	r.ToolChoice = v["tool_choice"]
	if n, ok := numInt(v["max_tokens"]); ok {
		r.MaxTokens = n
	}
	if n, ok := numInt(v["max_output_tokens"]); ok {
		r.MaxTokens = n
	}
	r.ResponseFormat = v["response_format"]
	if r.ResponseFormat == nil {
		if t, ok := v["text"].(map[string]any); ok {
			r.ResponseFormat = t["format"]
		}
	}

	switch apiType {
	case ChatCompletions:
		r.Messages = parseChatMessages(v["messages"])
		filtered := r.Messages[:0]
		for _, msg := range r.Messages {
			if msg.Role == "system" || msg.Role == "developer" {
				if text := partsText(msg.Parts); text != "" {
					if r.System != "" {
						r.System += "\n"
					}
					r.System += text
				}
				continue
			}
			filtered = append(filtered, msg)
		}
		r.Messages = filtered
		r.Tools = parseChatTools(v["tools"])
	case Messages:
		r.System = textValue(v["system"])
		r.Messages = parseAnthropicMessages(v["messages"])
		r.Tools = parseAnthropicTools(v["tools"])
	case Responses:
		r.System = textValue(v["instructions"])
		r.Messages = parseResponsesInput(v["input"])
		r.Tools = parseResponsesTools(v["tools"])
	default:
		return Request{}, fmt.Errorf("unsupported request protocol %q", apiType)
	}
	return r, nil
}

func encodeRequestBuiltin(apiType string, r Request) ([]byte, error) {
	switch apiType {
	case ChatCompletions:
		v := map[string]any{"model": r.Model, "messages": encodeChatMessages(r), "stream": r.Stream}
		if r.MaxTokens > 0 {
			v["max_tokens"] = r.MaxTokens
		}
		if r.Temperature != nil {
			v["temperature"] = r.Temperature
		}
		if len(r.Tools) > 0 {
			v["tools"] = encodeChatTools(r.Tools)
		}
		if r.ToolChoice != nil {
			v["tool_choice"] = normalizeToolChoice(r.ToolChoice, ChatCompletions)
		}
		if r.ParallelTools != nil {
			v["parallel_tool_calls"] = *r.ParallelTools
		}
		if r.ResponseFormat != nil {
			v["response_format"] = normalizeResponseFormat(r.ResponseFormat, ChatCompletions)
		}
		if r.Reasoning != nil {
			if s, ok := r.Reasoning.(string); ok {
				v["reasoning_effort"] = s
			} else {
				v["reasoning"] = r.Reasoning
			}
		}
		return json.Marshal(v)
	case Messages:
		v := map[string]any{"model": r.Model, "messages": encodeAnthropicMessages(r), "stream": r.Stream}
		if r.System != "" {
			v["system"] = r.System
		}
		if r.MaxTokens > 0 {
			v["max_tokens"] = r.MaxTokens
		} else {
			v["max_tokens"] = 1024
		}
		if r.Temperature != nil {
			v["temperature"] = r.Temperature
		}
		if len(r.Tools) > 0 {
			v["tools"] = encodeAnthropicTools(r.Tools)
		}
		if r.ToolChoice != nil {
			v["tool_choice"] = normalizeToolChoice(r.ToolChoice, Messages)
		}
		if r.Reasoning != nil {
			// Anthropic-compatible gateways that expose thinking accept this shape.
			switch x := r.Reasoning.(type) {
			case map[string]any:
				v["thinking"] = x
			case string:
				if x != "none" {
					v["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 1024}
				}
			}
		}
		return json.Marshal(v)
	case Responses:
		v := map[string]any{"model": r.Model, "input": encodeResponsesInput(r), "stream": r.Stream}
		if r.System != "" {
			v["instructions"] = r.System
		}
		if r.MaxTokens > 0 {
			v["max_output_tokens"] = r.MaxTokens
		}
		if r.Temperature != nil {
			v["temperature"] = r.Temperature
		}
		if len(r.Tools) > 0 {
			v["tools"] = encodeResponsesTools(r.Tools)
		}
		if r.ToolChoice != nil {
			v["tool_choice"] = normalizeToolChoice(r.ToolChoice, Responses)
		}
		if r.ResponseFormat != nil {
			v["text"] = map[string]any{"format": normalizeResponseFormat(r.ResponseFormat, Responses)}
		}
		if r.Reasoning != nil {
			v["reasoning"] = r.Reasoning
		}
		if r.ParallelTools != nil {
			v["parallel_tool_calls"] = *r.ParallelTools
		}
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("unsupported request protocol %q", apiType)
	}
}

func parseResponseBuiltin(apiType string, body []byte) (Response, error) {
	body = NormalizeAgentJSON(body)
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return Response{}, err
	}
	var r Response
	r.ID, _ = v["id"].(string)
	r.Model, _ = v["model"].(string)
	switch apiType {
	case ChatCompletions:
		choices, _ := v["choices"].([]any)
		if len(choices) > 0 {
			c, _ := choices[0].(map[string]any)
			r.FinishReason, _ = c["finish_reason"].(string)
			msg, _ := c["message"].(map[string]any)
			r.Text = textValue(msg["content"])
			r.Reasoning = firstNonEmpty(textValue(msg["reasoning_content"]), textValue(msg["reasoning"]))
			r.ToolCalls = parseChatToolCalls(msg["tool_calls"])
		}
	case Messages:
		r.FinishReason, _ = v["stop_reason"].(string)
		if blocks, ok := v["content"].([]any); ok {
			var texts []string
			for _, raw := range blocks {
				b, _ := raw.(map[string]any)
				switch b["type"] {
				case "text":
					texts = append(texts, textValue(b["text"]))
				case "thinking":
					r.Reasoning = firstNonEmpty(textValue(b["thinking"]), textValue(b["text"]))
				case "tool_use":
					args, _ := json.Marshal(b["input"])
					r.ToolCalls = append(r.ToolCalls, ToolCall{ID: textValue(b["id"]), Name: textValue(b["name"]), Arguments: string(args)})
				}
			}
			r.Text = strings.Join(texts, "")
		}
	case Responses:
		if s, ok := v["output_text"].(string); ok {
			r.Text = s
		}
		out, _ := v["output"].([]any)
		var texts []string
		if r.Text != "" {
			texts = append(texts, r.Text)
		}
		for _, raw := range out {
			item, _ := raw.(map[string]any)
			typ := textValue(item["type"])
			switch typ {
			case "message":
				if content, ok := item["content"].([]any); ok {
					for _, cr := range content {
						c, _ := cr.(map[string]any)
						if textValue(c["type"]) == "output_text" || textValue(c["type"]) == "text" {
							texts = append(texts, textValue(c["text"]))
						}
					}
				}
			case "reasoning":
				if summary, ok := item["summary"].([]any); ok {
					var rs []string
					for _, sr := range summary {
						if sm, ok := sr.(map[string]any); ok {
							rs = append(rs, textValue(sm["text"]))
						}
					}
					r.Reasoning = strings.Join(rs, "")
				}
			case "function_call":
				r.ToolCalls = append(r.ToolCalls, ToolCall{ID: firstNonEmpty(textValue(item["call_id"]), textValue(item["id"])), Name: textValue(item["name"]), Arguments: repairArguments(textValue(item["arguments"]))})
			}
		}
		r.Text = strings.Join(uniqueStrings(texts), "")
		if len(r.ToolCalls) > 0 {
			r.FinishReason = "tool_calls"
		} else {
			r.FinishReason = "stop"
		}
	default:
		return Response{}, fmt.Errorf("unsupported response protocol %q", apiType)
	}
	r.InputTokens, r.OutputTokens, r.TotalTokens = usage(v)
	return r, nil
}

func encodeResponseBuiltin(apiType string, r Response) ([]byte, error) {
	if r.TotalTokens == 0 {
		r.TotalTokens = r.InputTokens + r.OutputTokens
	}
	switch apiType {
	case ChatCompletions:
		msg := map[string]any{"role": "assistant", "content": r.Text}
		if r.Reasoning != "" {
			msg["reasoning_content"] = r.Reasoning
		}
		if len(r.ToolCalls) > 0 {
			msg["tool_calls"] = encodeChatToolCalls(r.ToolCalls)
		}
		finish := mapFinishReason(r.FinishReason, ChatCompletions, len(r.ToolCalls) > 0)
		return json.Marshal(map[string]any{"id": fallbackID(r.ID, "chatcmpl-omnibridge"), "object": "chat.completion", "model": r.Model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": r.InputTokens, "completion_tokens": r.OutputTokens, "total_tokens": r.TotalTokens}})
	case Messages:
		content := []any{}
		if r.Reasoning != "" {
			content = append(content, map[string]any{"type": "thinking", "thinking": r.Reasoning})
		}
		if r.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": r.Text})
		}
		for _, tc := range r.ToolCalls {
			var input any = map[string]any{}
			if json.Unmarshal([]byte(repairArguments(tc.Arguments)), &input) != nil {
				input = map[string]any{"_raw": tc.Arguments}
			}
			content = append(content, map[string]any{"type": "tool_use", "id": fallbackID(tc.ID, "toolu_omnibridge"), "name": tc.Name, "input": input})
		}
		return json.Marshal(map[string]any{"id": fallbackID(r.ID, "msg_omnibridge"), "type": "message", "role": "assistant", "model": r.Model, "content": content, "stop_reason": mapFinishReason(r.FinishReason, Messages, len(r.ToolCalls) > 0), "usage": map[string]any{"input_tokens": r.InputTokens, "output_tokens": r.OutputTokens}})
	case Responses:
		output := []any{}
		if r.Reasoning != "" {
			output = append(output, map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": r.Reasoning}}})
		}
		if r.Text != "" {
			output = append(output, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": r.Text}}})
		}
		for _, tc := range r.ToolCalls {
			output = append(output, map[string]any{"type": "function_call", "id": fallbackID(tc.ID, "fc_omnibridge"), "call_id": fallbackID(tc.ID, "call_omnibridge"), "name": tc.Name, "arguments": repairArguments(tc.Arguments)})
		}
		return json.Marshal(map[string]any{"id": fallbackID(r.ID, "resp_omnibridge"), "object": "response", "status": "completed", "model": r.Model, "output": output, "output_text": r.Text, "usage": map[string]any{"input_tokens": r.InputTokens, "output_tokens": r.OutputTokens, "total_tokens": r.TotalTokens}})
	default:
		return nil, fmt.Errorf("unsupported response protocol %q", apiType)
	}
}

// SyntheticSSE creates a valid one-shot downstream stream for a response that
// had to be buffered for cross-protocol translation. It preserves tool calls,
// finish reasons and usage, although it cannot preserve native token timing.
func SyntheticSSE(apiType string, body []byte) ([]byte, error) {
	res, err := ParseResponse(apiType, body)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	switch apiType {
	case ChatCompletions:
		delta := map[string]any{"role": "assistant"}
		if res.Text != "" {
			delta["content"] = res.Text
		}
		if len(res.ToolCalls) > 0 {
			delta["tool_calls"] = encodeChatToolCalls(res.ToolCalls)
		}
		chunk := map[string]any{"id": fallbackID(res.ID, "chatcmpl-omnibridge"), "object": "chat.completion.chunk", "model": res.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": mapFinishReason(res.FinishReason, ChatCompletions, len(res.ToolCalls) > 0)}}, "usage": map[string]any{"prompt_tokens": res.InputTokens, "completion_tokens": res.OutputTokens, "total_tokens": res.TotalTokens}}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(&buf, "data: %s\n\ndata: [DONE]\n\n", b)
	case Messages:
		start := map[string]any{"type": "message_start", "message": map[string]any{"id": fallbackID(res.ID, "msg_omnibridge"), "type": "message", "role": "assistant", "model": res.Model, "content": []any{}, "usage": map[string]any{"input_tokens": res.InputTokens, "output_tokens": 0}}}
		b, _ := json.Marshal(start)
		fmt.Fprintf(&buf, "event: message_start\ndata: %s\n\n", b)
		idx := 0
		if res.Text != "" {
			x, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": res.Text}})
			fmt.Fprintf(&buf, "event: content_block_start\ndata: %s\n\n", x)
			idx++
		}
		for _, tc := range res.ToolCalls {
			var input any
			_ = json.Unmarshal([]byte(repairArguments(tc.Arguments)), &input)
			x, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": fallbackID(tc.ID, "toolu_omnibridge"), "name": tc.Name, "input": input}})
			fmt.Fprintf(&buf, "event: content_block_start\ndata: %s\n\n", x)
			idx++
		}
		stop, _ := json.Marshal(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": mapFinishReason(res.FinishReason, Messages, len(res.ToolCalls) > 0)}, "usage": map[string]any{"output_tokens": res.OutputTokens}})
		fmt.Fprintf(&buf, "event: message_delta\ndata: %s\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", stop)
	case Responses:
		created, _ := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": fallbackID(res.ID, "resp_omnibridge"), "object": "response", "status": "in_progress", "model": res.Model}})
		fmt.Fprintf(&buf, "event: response.created\ndata: %s\n\n", created)
		completed, _ := EncodeResponse(Responses, res)
		var cm map[string]any
		_ = json.Unmarshal(completed, &cm)
		ev, _ := json.Marshal(map[string]any{"type": "response.completed", "response": cm})
		fmt.Fprintf(&buf, "event: response.completed\ndata: %s\n\n", ev)
	default:
		return nil, errors.New("unsupported synthetic stream protocol")
	}
	return buf.Bytes(), nil
}

func parseChatMessages(v any) []Message {
	arr, _ := v.([]any)
	out := []Message{}
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		role := textValue(m["role"])
		cm := Message{Role: role, ToolCallID: textValue(m["tool_call_id"])}
		cm.Parts = parseContentParts(m["content"])
		cm.ToolCalls = parseChatToolCalls(m["tool_calls"])
		cm.Reasoning = firstNonEmpty(textValue(m["reasoning_content"]), textValue(m["reasoning"]))
		out = append(out, cm)
	}
	return out
}
func parseAnthropicMessages(v any) []Message {
	arr, _ := v.([]any)
	out := []Message{}
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		role := textValue(m["role"])
		cm := Message{Role: role}
		if blocks, ok := m["content"].([]any); ok {
			for _, br := range blocks {
				b, _ := br.(map[string]any)
				switch textValue(b["type"]) {
				case "text":
					cm.Parts = append(cm.Parts, Part{Type: "text", Text: textValue(b["text"])})
				case "thinking":
					cm.Reasoning = firstNonEmpty(textValue(b["thinking"]), textValue(b["text"]))
					cm.ReasoningSignature = textValue(b["signature"])
				case "image":
					if src, ok := b["source"].(map[string]any); ok {
						cm.Parts = append(cm.Parts, Part{Type: "image", MediaType: textValue(src["media_type"]), Data: textValue(src["data"])})
					}
				case "tool_use":
					args, _ := json.Marshal(b["input"])
					cm.ToolCalls = append(cm.ToolCalls, ToolCall{ID: textValue(b["id"]), Name: textValue(b["name"]), Arguments: string(args)})
				case "tool_result":
					out = append(out, Message{Role: "tool", ToolCallID: textValue(b["tool_use_id"]), Parts: []Part{{Type: "text", Text: textValue(b["content"])}}})
				}
			}
		} else {
			cm.Parts = parseContentParts(m["content"])
		}
		if len(cm.Parts) > 0 || len(cm.ToolCalls) > 0 {
			out = append(out, cm)
		}
	}
	return out
}
func parseResponsesInput(v any) []Message {
	if s, ok := v.(string); ok {
		return []Message{{Role: "user", Parts: []Part{{Type: "text", Text: s}}}}
	}
	arr, _ := v.([]any)
	out := []Message{}
	for _, raw := range arr {
		item, _ := raw.(map[string]any)
		typ := textValue(item["type"])
		switch typ {
		case "message", "":
			role := textValue(item["role"])
			if role == "" {
				role = "user"
			}
			out = append(out, Message{Role: role, Parts: parseResponsesContent(item["content"])})
		case "function_call":
			out = append(out, Message{Role: "assistant", ToolCalls: []ToolCall{{ID: firstNonEmpty(textValue(item["call_id"]), textValue(item["id"])), Name: textValue(item["name"]), Arguments: repairArguments(textValue(item["arguments"]))}}})
		case "function_call_output":
			out = append(out, Message{Role: "tool", ToolCallID: textValue(item["call_id"]), Parts: []Part{{Type: "text", Text: textValue(item["output"])}}})
		}
	}
	return out
}
func parseContentParts(v any) []Part {
	if s, ok := v.(string); ok {
		return []Part{{Type: "text", Text: s}}
	}
	arr, _ := v.([]any)
	out := []Part{}
	for _, raw := range arr {
		p, _ := raw.(map[string]any)
		typ := textValue(p["type"])
		switch typ {
		case "text", "input_text", "output_text":
			out = append(out, Part{Type: "text", Text: textValue(p["text"])})
		case "image_url":
			if im, ok := p["image_url"].(map[string]any); ok {
				out = append(out, Part{Type: "image", ImageURL: textValue(im["url"])})
			}
		case "input_image":
			out = append(out, Part{Type: "image", ImageURL: firstNonEmpty(textValue(p["image_url"]), textValue(p["url"]))})
		}
	}
	return out
}
func parseResponsesContent(v any) []Part { return parseContentParts(v) }
func parseChatTools(v any) []Tool {
	arr, _ := v.([]any)
	out := []Tool{}
	for _, raw := range arr {
		x, _ := raw.(map[string]any)
		f, _ := x["function"].(map[string]any)
		if f == nil {
			f = x
		}
		name := textValue(f["name"])
		if name != "" {
			out = append(out, Tool{Name: name, Description: textValue(f["description"]), Parameters: f["parameters"]})
		}
	}
	return out
}
func parseAnthropicTools(v any) []Tool {
	arr, _ := v.([]any)
	out := []Tool{}
	for _, raw := range arr {
		x, _ := raw.(map[string]any)
		if n := textValue(x["name"]); n != "" {
			out = append(out, Tool{Name: n, Description: textValue(x["description"]), Parameters: x["input_schema"]})
		}
	}
	return out
}
func parseResponsesTools(v any) []Tool { return parseChatTools(v) }
func parseChatToolCalls(v any) []ToolCall {
	arr, _ := v.([]any)
	out := []ToolCall{}
	for _, raw := range arr {
		x, _ := raw.(map[string]any)
		f, _ := x["function"].(map[string]any)
		if f == nil {
			f = x
		}
		name := textValue(f["name"])
		if name != "" {
			out = append(out, ToolCall{ID: firstNonEmpty(textValue(x["id"]), textValue(x["call_id"])), Name: name, Arguments: repairArguments(textValue(f["arguments"]))})
		}
	}
	return out
}

func encodeChatMessages(r Request) []any {
	out := []any{}
	if r.System != "" {
		out = append(out, map[string]any{"role": "system", "content": r.System})
	}
	for _, m := range r.Messages {
		x := map[string]any{"role": m.Role}
		if m.Role == "tool" {
			x["tool_call_id"] = m.ToolCallID
			x["content"] = partsText(m.Parts)
		} else {
			x["content"] = encodeChatParts(m.Parts)
			if m.Role == "assistant" && m.Reasoning != "" {
				x["reasoning_content"] = m.Reasoning
			}
			if len(m.ToolCalls) > 0 {
				x["tool_calls"] = encodeChatToolCalls(m.ToolCalls)
			}
		}
		out = append(out, x)
	}
	return out
}
func encodeAnthropicMessages(r Request) []any {
	out := []any{}
	for _, m := range r.Messages {
		if m.Role == "system" {
			continue
		}
		if m.Role == "tool" {
			out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": partsText(m.Parts)}}})
			continue
		}
		blocks := []any{}
		if m.Role == "assistant" && m.Reasoning != "" {
			thinking := map[string]any{"type": "thinking", "thinking": m.Reasoning}
			if m.ReasoningSignature != "" {
				thinking["signature"] = m.ReasoningSignature
			}
			blocks = append(blocks, thinking)
		}
		for _, p := range m.Parts {
			if p.Type == "image" {
				media, data := imageData(p)
				if data != "" {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": media, "data": data}})
				}
			} else if p.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		}
		for _, tc := range m.ToolCalls {
			var input any = map[string]any{}
			_ = json.Unmarshal([]byte(repairArguments(tc.Arguments)), &input)
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": fallbackID(tc.ID, "toolu_omnibridge"), "name": tc.Name, "input": input})
		}
		out = append(out, map[string]any{"role": mapRoleAnthropic(m.Role), "content": blocks})
	}
	return out
}
func encodeResponsesInput(r Request) []any {
	out := []any{}
	for _, m := range r.Messages {
		if m.Role == "system" {
			continue
		}
		if m.Role == "tool" {
			out = append(out, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": partsText(m.Parts)})
			continue
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				out = append(out, map[string]any{"type": "function_call", "call_id": fallbackID(tc.ID, "call_omnibridge"), "name": tc.Name, "arguments": repairArguments(tc.Arguments)})
			}
		}
		if len(m.Parts) > 0 {
			out = append(out, map[string]any{"type": "message", "role": m.Role, "content": encodeResponsesParts(m.Parts)})
		}
	}
	return out
}
func encodeChatParts(parts []Part) any {
	if len(parts) == 0 {
		return ""
	}
	onlyText := true
	for _, p := range parts {
		if p.Type == "image" {
			onlyText = false
		}
	}
	if onlyText {
		return partsText(parts)
	}
	out := []any{}
	for _, p := range parts {
		if p.Type == "image" {
			u := p.ImageURL
			if u == "" && p.Data != "" {
				media := p.MediaType
				if media == "" {
					media = "image/png"
				}
				u = "data:" + media + ";base64," + p.Data
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
		} else if p.Text != "" {
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		}
	}
	return out
}
func encodeResponsesParts(parts []Part) []any {
	out := []any{}
	for _, p := range parts {
		if p.Type == "image" {
			u := p.ImageURL
			if u == "" && p.Data != "" {
				media := p.MediaType
				if media == "" {
					media = "image/png"
				}
				u = "data:" + media + ";base64," + p.Data
			}
			out = append(out, map[string]any{"type": "input_image", "image_url": u})
		} else if p.Text != "" {
			out = append(out, map[string]any{"type": "input_text", "text": p.Text})
		}
	}
	return out
}
func encodeChatTools(tools []Tool) []any {
	out := []any{}
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": params}})
	}
	return out
}
func encodeAnthropicTools(tools []Tool) []any {
	out := []any{}
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"name": t.Name, "description": t.Description, "input_schema": params})
	}
	return out
}
func encodeResponsesTools(tools []Tool) []any {
	out := []any{}
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": params})
	}
	return out
}
func encodeChatToolCalls(calls []ToolCall) []any {
	out := []any{}
	for i, tc := range calls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_omnibridge_%d", i)
		}
		out = append(out, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": repairArguments(tc.Arguments)}})
	}
	return out
}

func normalizeResponseFormat(v any, to string) any {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return v
	}
	typ := textValue(m["type"])
	if typ != "json_schema" {
		return v
	}
	if to == ChatCompletions {
		if _, ok := m["json_schema"]; ok {
			return m
		}
		js := map[string]any{}
		for _, k := range []string{"name", "schema", "strict", "description"} {
			if x, ok := m[k]; ok {
				js[k] = x
			}
		}
		if schema, changed := coerceSchema(js["schema"]); changed || schema != nil {
			js["schema"] = schema
		}
		return map[string]any{"type": "json_schema", "json_schema": js}
	}
	if to == Responses {
		if js, ok := m["json_schema"].(map[string]any); ok {
			out := map[string]any{"type": "json_schema"}
			for _, k := range []string{"name", "schema", "strict", "description"} {
				if x, ok := js[k]; ok {
					out[k] = x
				}
			}
			if schema, changed := coerceSchema(out["schema"]); changed || schema != nil {
				out["schema"] = schema
			}
			return out
		}
		out := map[string]any{}
		for k, x := range m {
			out[k] = x
		}
		if schema, changed := coerceSchema(out["schema"]); changed || schema != nil {
			out["schema"] = schema
		}
		return out
	}
	return v
}

func normalizeToolChoice(v any, to string) any {
	if s, ok := v.(string); ok {
		return s
	}
	m, _ := v.(map[string]any)
	if m == nil {
		return v
	}
	var name string
	if f, ok := m["function"].(map[string]any); ok {
		name = textValue(f["name"])
	}
	if name == "" {
		name = textValue(m["name"])
	}
	if name == "" {
		return v
	}
	if to == Messages {
		return map[string]any{"type": "tool", "name": name}
	}
	if to == Responses {
		return map[string]any{"type": "function", "name": name}
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name}}
}
func mapRoleAnthropic(role string) string {
	if role == "assistant" {
		return "assistant"
	}
	return "user"
}
func partsText(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
func textValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, it := range x {
			if m, ok := it.(map[string]any); ok {
				b.WriteString(textValue(m["text"]))
			}
		}
		return b.String()
	default:
		return ""
	}
}
func numInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}
func usage(v map[string]any) (int64, int64, int64) {
	u, _ := v["usage"].(map[string]any)
	if u == nil {
		return 0, 0, 0
	}
	n := func(keys ...string) int64 {
		for _, k := range keys {
			if x, ok := u[k].(float64); ok {
				return int64(x)
			}
		}
		return 0
	}
	in := n("prompt_tokens", "input_tokens")
	out := n("completion_tokens", "output_tokens")
	total := n("total_tokens")
	if total == 0 {
		total = in + out
	}
	return in, out, total
}
func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
func fallbackID(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func mapFinishReason(reason, to string, hasTools bool) string {
	if hasTools {
		if to == Messages {
			return "tool_use"
		}
		return "tool_calls"
	}
	r := strings.ToLower(reason)
	if to == Messages {
		switch r {
		case "length", "max_tokens":
			return "max_tokens"
		case "stop_sequence":
			return "stop_sequence"
		default:
			return "end_turn"
		}
	}
	switch r {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		if r == "" {
			return "stop"
		}
		return r
	}
}

var trailingComma = regexp.MustCompile(`,\s*([}\]])`)

func RepairArguments(s string) string { return repairArguments(s) }

func repairArguments(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if json.Valid([]byte(s)) {
		return s
	}
	x := trailingComma.ReplaceAllString(s, "$1")
	if json.Valid([]byte(x)) {
		return x
	}
	return s
}

// NormalizeAgentJSON repairs common tool-call argument defects without changing
// normal response payloads. Invalid argument strings are only modified when a
// safe syntactic repair (code-fence/trailing-comma removal) produces valid JSON.
func NormalizeAgentJSON(body []byte) []byte {
	var v any
	candidate := body
	if json.Unmarshal(candidate, &v) != nil {
		// A surprisingly common OpenAI-compatible defect is double-escaping
		// quotes inside function.arguments, producing {\\"x\\":1}.
		// Only attempt this conservative repair when the original document is
		// invalid, and accept it only if the full JSON then parses successfully.
		repaired := bytes.ReplaceAll(candidate, []byte{'\\', '\\', '"'}, []byte{'\\', '"'})
		if json.Unmarshal(repaired, &v) != nil {
			return body
		}
		candidate = repaired
	}
	walkNormalize(v)
	out, err := json.Marshal(v)
	if err != nil {
		return candidate
	}
	return out
}
func walkNormalize(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "arguments" {
				if s, ok := val.(string); ok {
					x[k] = repairArguments(s)
				}
			}
			walkNormalize(x[k])
		}
	case []any:
		for _, it := range x {
			walkNormalize(it)
		}
	}
}

func imageData(p Part) (string, string) {
	if p.Data != "" {
		m := p.MediaType
		if m == "" {
			m = "image/png"
		}
		return m, p.Data
	}
	if strings.HasPrefix(p.ImageURL, "data:") {
		parts := strings.SplitN(strings.TrimPrefix(p.ImageURL, "data:"), ",", 2)
		if len(parts) == 2 {
			meta := strings.TrimSuffix(parts[0], ";base64")
			return meta, parts[1]
		}
	}
	return "", ""
}
