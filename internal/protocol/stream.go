package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	StreamStart          = "start"
	StreamTextDelta      = "text_delta"
	StreamReasoningDelta = "reasoning_delta"
	StreamToolStart      = "tool_start"
	StreamToolDelta      = "tool_delta"
	StreamFinish         = "finish"
	StreamUsage          = "usage"
	StreamError          = "error"
	StreamDone           = "done"
)

// SSEMessage is the parsed wire-level Server-Sent Event frame.
type SSEMessage struct {
	Event string
	Data  []byte
}

// ReadSSE incrementally parses one SSE event at a time without buffering the
// complete upstream response. Multiple data: lines are joined with '\n'.
func ReadSSE(r io.Reader, fn func(SSEMessage) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var event string
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 && event == "" {
			return nil
		}
		raw := append([]byte(nil), data.Bytes()...)
		if len(raw) > 0 && raw[len(raw)-1] == '\n' {
			raw = raw[:len(raw)-1]
		}
		msg := SSEMessage{Event: event, Data: raw}
		event = ""
		data.Reset()
		return fn(msg)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if e := flush(); e != nil {
					return e
				}
			} else if strings.HasPrefix(line, ":") {
				// comment / keepalive
			} else if k, v, ok := strings.Cut(line, ":"); ok {
				if strings.HasPrefix(v, " ") {
					v = v[1:]
				}
				switch k {
				case "event":
					event = v
				case "data":
					data.WriteString(v)
					data.WriteByte('\n')
				}
			} else if line == "data" {
				data.WriteByte('\n')
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if e := flush(); e != nil {
					return e
				}
				return nil
			}
			return err
		}
	}
}

type StreamDecoder struct {
	Protocol string
	id       string
	model    string
	toolMeta map[int]CanonicalToolCall
}

func NewStreamDecoder(protocol string) *StreamDecoder {
	return &StreamDecoder{Protocol: protocol, toolMeta: map[int]CanonicalToolCall{}}
}

func (d *StreamDecoder) DecodeSSE(msg SSEMessage) ([]CanonicalStreamEvent, error) {
	data := bytes.TrimSpace(msg.Data)
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		return []CanonicalStreamEvent{{Type: StreamDone, Done: true, ID: d.id, Model: d.model}}, nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid %s stream JSON: %q", d.Protocol, string(data))
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	switch d.Protocol {
	case ChatCompletions:
		return d.decodeChat(v)
	case Messages:
		return d.decodeMessages(msg.Event, v)
	case Responses:
		return d.decodeResponses(msg.Event, v)
	default:
		return nil, fmt.Errorf("unsupported stream protocol %q", d.Protocol)
	}
}

func (d *StreamDecoder) startEvent(id, model string) CanonicalStreamEvent {
	if id != "" {
		d.id = id
	}
	if model != "" {
		d.model = model
	}
	return CanonicalStreamEvent{Type: StreamStart, ID: d.id, Model: d.model}
}

func (d *StreamDecoder) decodeChat(v map[string]any) ([]CanonicalStreamEvent, error) {
	id := textValue(v["id"])
	model := textValue(v["model"])
	out := []CanonicalStreamEvent{}
	if d.id == "" || d.model == "" {
		out = append(out, d.startEvent(id, model))
	} else {
		if id != "" {
			d.id = id
		}
		if model != "" {
			d.model = model
		}
	}
	if u := canonicalUsageFromAny(v["usage"]); u.TotalTokens != 0 || u.InputTokens != 0 || u.OutputTokens != 0 {
		out = append(out, CanonicalStreamEvent{Type: StreamUsage, ID: d.id, Model: d.model, Usage: u})
	}
	choices, _ := v["choices"].([]any)
	for _, raw := range choices {
		c, _ := raw.(map[string]any)
		delta, _ := c["delta"].(map[string]any)
		if txt := textValue(delta["content"]); txt != "" {
			out = append(out, CanonicalStreamEvent{Type: StreamTextDelta, ID: d.id, Model: d.model, TextDelta: txt})
		}
		if r := firstNonEmpty(textValue(delta["reasoning_content"]), textValue(delta["reasoning"])); r != "" {
			out = append(out, CanonicalStreamEvent{Type: StreamReasoningDelta, ID: d.id, Model: d.model, Reasoning: r})
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, tr := range tcs {
				tm, _ := tr.(map[string]any)
				idx := intFromAny(tm["index"])
				fn, _ := tm["function"].(map[string]any)
				meta := d.toolMeta[idx]
				if x := textValue(tm["id"]); x != "" {
					meta.ID = x
				}
				if x := textValue(fn["name"]); x != "" {
					meta.Name = x
				}
				if meta.ID == "" {
					meta.ID = fmt.Sprintf("call_omnibridge_%d", idx)
				}
				d.toolMeta[idx] = meta
				if textValue(tm["id"]) != "" || textValue(fn["name"]) != "" {
					out = append(out, CanonicalStreamEvent{Type: StreamToolStart, ID: d.id, Model: d.model, Index: idx, ToolCallID: meta.ID, ToolName: meta.Name})
				}
				if a := textValue(fn["arguments"]); a != "" {
					out = append(out, CanonicalStreamEvent{Type: StreamToolDelta, ID: d.id, Model: d.model, Index: idx, ToolCallID: meta.ID, ToolName: meta.Name, ArgumentsDelta: a})
				}
			}
		}
		if fr := textValue(c["finish_reason"]); fr != "" {
			out = append(out, CanonicalStreamEvent{Type: StreamFinish, ID: d.id, Model: d.model, FinishReason: fr})
		}
	}
	return out, nil
}

func (d *StreamDecoder) decodeMessages(eventName string, v map[string]any) ([]CanonicalStreamEvent, error) {
	typ := firstNonEmpty(textValue(v["type"]), eventName)
	out := []CanonicalStreamEvent{}
	switch typ {
	case "message_start":
		m, _ := v["message"].(map[string]any)
		ev := d.startEvent(textValue(m["id"]), textValue(m["model"]))
		out = append(out, ev)
		if u := canonicalUsageFromAny(m["usage"]); u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0 {
			out = append(out, CanonicalStreamEvent{Type: StreamUsage, ID: d.id, Model: d.model, Usage: u})
		}
	case "content_block_start":
		idx := intFromAny(v["index"])
		b, _ := v["content_block"].(map[string]any)
		switch textValue(b["type"]) {
		case "text":
			if txt := textValue(b["text"]); txt != "" {
				out = append(out, CanonicalStreamEvent{Type: StreamTextDelta, ID: d.id, Model: d.model, Index: idx, TextDelta: txt})
			}
		case "thinking":
			if txt := firstNonEmpty(textValue(b["thinking"]), textValue(b["text"])); txt != "" {
				out = append(out, CanonicalStreamEvent{Type: StreamReasoningDelta, ID: d.id, Model: d.model, Index: idx, Reasoning: txt})
			}
		case "tool_use":
			tc := CanonicalToolCall{ID: textValue(b["id"]), Name: textValue(b["name"])}
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("toolu_omnibridge_%d", idx)
			}
			d.toolMeta[idx] = tc
			out = append(out, CanonicalStreamEvent{Type: StreamToolStart, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name})
			if input, ok := b["input"]; ok {
				if raw, err := json.Marshal(input); err == nil && string(raw) != "{}" && string(raw) != "null" {
					out = append(out, CanonicalStreamEvent{Type: StreamToolDelta, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name, ArgumentsDelta: string(raw)})
				}
			}
		}
	case "content_block_delta":
		idx := intFromAny(v["index"])
		delta, _ := v["delta"].(map[string]any)
		switch textValue(delta["type"]) {
		case "text_delta":
			out = append(out, CanonicalStreamEvent{Type: StreamTextDelta, ID: d.id, Model: d.model, Index: idx, TextDelta: textValue(delta["text"])})
		case "thinking_delta":
			out = append(out, CanonicalStreamEvent{Type: StreamReasoningDelta, ID: d.id, Model: d.model, Index: idx, Reasoning: firstNonEmpty(textValue(delta["thinking"]), textValue(delta["text"]))})
		case "input_json_delta":
			tc := d.toolMeta[idx]
			out = append(out, CanonicalStreamEvent{Type: StreamToolDelta, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name, ArgumentsDelta: textValue(delta["partial_json"])})
		}
	case "message_delta":
		delta, _ := v["delta"].(map[string]any)
		if fr := textValue(delta["stop_reason"]); fr != "" {
			out = append(out, CanonicalStreamEvent{Type: StreamFinish, ID: d.id, Model: d.model, FinishReason: fr})
		}
		if u := canonicalUsageFromAny(v["usage"]); u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0 {
			out = append(out, CanonicalStreamEvent{Type: StreamUsage, ID: d.id, Model: d.model, Usage: u})
		}
	case "message_stop":
		out = append(out, CanonicalStreamEvent{Type: StreamDone, Done: true, ID: d.id, Model: d.model})
	case "error":
		out = append(out, CanonicalStreamEvent{Type: StreamError, ID: d.id, Model: d.model, Error: compactJSON(v)})
	}
	return out, nil
}

func (d *StreamDecoder) decodeResponses(eventName string, v map[string]any) ([]CanonicalStreamEvent, error) {
	typ := firstNonEmpty(textValue(v["type"]), eventName)
	out := []CanonicalStreamEvent{}
	response, _ := v["response"].(map[string]any)
	if typ == "response.created" || typ == "response.in_progress" {
		out = append(out, d.startEvent(textValue(response["id"]), textValue(response["model"])))
	}
	switch typ {
	case "response.output_text.delta":
		out = append(out, CanonicalStreamEvent{Type: StreamTextDelta, ID: d.id, Model: d.model, Index: intFromAny(v["output_index"]), TextDelta: textValue(v["delta"])})
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		out = append(out, CanonicalStreamEvent{Type: StreamReasoningDelta, ID: d.id, Model: d.model, Index: intFromAny(v["output_index"]), Reasoning: textValue(v["delta"])})
	case "response.output_item.added":
		idx := intFromAny(v["output_index"])
		item, _ := v["item"].(map[string]any)
		if textValue(item["type"]) == "function_call" {
			tc := CanonicalToolCall{ID: firstNonEmpty(textValue(item["call_id"]), textValue(item["id"])), Name: textValue(item["name"])}
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_omnibridge_%d", idx)
			}
			d.toolMeta[idx] = tc
			out = append(out, CanonicalStreamEvent{Type: StreamToolStart, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name})
			if a := textValue(item["arguments"]); a != "" {
				out = append(out, CanonicalStreamEvent{Type: StreamToolDelta, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name, ArgumentsDelta: a})
			}
		}
	case "response.function_call_arguments.delta":
		idx := intFromAny(v["output_index"])
		tc := d.toolMeta[idx]
		out = append(out, CanonicalStreamEvent{Type: StreamToolDelta, ID: d.id, Model: d.model, Index: idx, ToolCallID: tc.ID, ToolName: tc.Name, ArgumentsDelta: textValue(v["delta"])})
	case "response.completed":
		if id := textValue(response["id"]); id != "" {
			d.id = id
		}
		if model := textValue(response["model"]); model != "" {
			d.model = model
		}
		if u := canonicalUsageFromAny(response["usage"]); u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0 {
			out = append(out, CanonicalStreamEvent{Type: StreamUsage, ID: d.id, Model: d.model, Usage: u})
		}
		out = append(out, CanonicalStreamEvent{Type: StreamFinish, ID: d.id, Model: d.model, FinishReason: "stop"}, CanonicalStreamEvent{Type: StreamDone, ID: d.id, Model: d.model, Done: true})
	case "response.failed", "error":
		out = append(out, CanonicalStreamEvent{Type: StreamError, ID: d.id, Model: d.model, Error: compactJSON(v)})
	}
	return out, nil
}

func canonicalUsageFromAny(v any) CanonicalUsage {
	m, _ := v.(map[string]any)
	if m == nil {
		return CanonicalUsage{}
	}
	in := int64FromKeys(m, "input_tokens", "prompt_tokens")
	out := int64FromKeys(m, "output_tokens", "completion_tokens")
	total := int64FromKeys(m, "total_tokens")
	// Streaming protocols often report input and output usage in separate events.
	// Do not synthesize a partial total here; the observer/encoder merges the
	// components and computes total only after both sides are known.
	return CanonicalUsage{InputTokens: in, OutputTokens: out, TotalTokens: total}
}

func int64FromKeys(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		switch x := m[k].(type) {
		case float64:
			return int64(x)
		case json.Number:
			n, _ := x.Int64()
			return n
		case int:
			return int64(x)
		case int64:
			return x
		case string:
			n, _ := strconv.ParseInt(x, 10, 64)
			if n != 0 {
				return n
			}
		}
	}
	return 0
}
func intFromAny(v any) int     { return int(int64FromKeys(map[string]any{"x": v}, "x")) }
func compactJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// StreamEncoder converts canonical events to one downstream protocol in real time.
type StreamEncoder struct {
	Protocol                 string
	id                       string
	model                    string
	started                  bool
	chatRoleSent             bool
	chatTools                map[string]int
	chatNext                 int
	anthropicBlocks          map[string]int
	anthropicNext            int
	anthropicOpen            map[int]bool
	responseItems            map[string]int
	responseNext             int
	responseTextStarted      bool
	responseReasoningStarted bool
	finishReason             string
	usage                    CanonicalUsage
}

func NewStreamEncoder(protocol string) *StreamEncoder {
	return &StreamEncoder{Protocol: protocol, chatTools: map[string]int{}, anthropicBlocks: map[string]int{}, anthropicOpen: map[int]bool{}, responseItems: map[string]int{}}
}

func (e *StreamEncoder) EncodeSSE(ev CanonicalStreamEvent) ([]byte, error) {
	if ev.ID != "" {
		e.id = ev.ID
	}
	if ev.Model != "" {
		e.model = ev.Model
	}
	if ev.Type == StreamUsage {
		e.usage = mergeUsage(e.usage, ev.Usage)
	}
	if ev.FinishReason != "" {
		e.finishReason = ev.FinishReason
	}
	switch e.Protocol {
	case ChatCompletions:
		return e.encodeChat(ev)
	case Messages:
		return e.encodeMessages(ev)
	case Responses:
		return e.encodeResponses(ev)
	default:
		return nil, fmt.Errorf("unsupported stream protocol %q", e.Protocol)
	}
}

func (e *StreamEncoder) chatToolIndex(id string, fallback int) int {
	key := strings.TrimSpace(id)
	if key == "" {
		key = fmt.Sprintf("idx:%d", fallback)
	}
	if idx, ok := e.chatTools[key]; ok {
		return idx
	}
	idx := e.chatNext
	e.chatNext++
	e.chatTools[key] = idx
	return idx
}

func (e *StreamEncoder) encodeChat(ev CanonicalStreamEvent) ([]byte, error) {
	id := fallbackID(e.id, "chatcmpl-omnibridge")
	model := e.model
	chunk := func(delta map[string]any, finish any, usage any) []byte {
		m := map[string]any{"id": id, "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if usage != nil {
			m["usage"] = usage
		}
		b, _ := json.Marshal(m)
		return []byte("data: " + string(b) + "\n\n")
	}
	switch ev.Type {
	case StreamStart:
		if e.started {
			return nil, nil
		}
		e.started = true
		e.chatRoleSent = true
		return chunk(map[string]any{"role": "assistant"}, nil, nil), nil
	case StreamTextDelta:
		d := map[string]any{"content": ev.TextDelta}
		if !e.chatRoleSent {
			d["role"] = "assistant"
			e.chatRoleSent = true
		}
		return chunk(d, nil, nil), nil
	case StreamReasoningDelta:
		d := map[string]any{"reasoning_content": ev.Reasoning}
		if !e.chatRoleSent {
			d["role"] = "assistant"
			e.chatRoleSent = true
		}
		return chunk(d, nil, nil), nil
	case StreamToolStart:
		idx := e.chatToolIndex(ev.ToolCallID, ev.Index)
		tc := map[string]any{"index": idx, "id": fallbackID(ev.ToolCallID, fmt.Sprintf("call_omnibridge_%d", idx)), "type": "function", "function": map[string]any{"name": ev.ToolName, "arguments": ""}}
		return chunk(map[string]any{"tool_calls": []any{tc}}, nil, nil), nil
	case StreamToolDelta:
		idx := e.chatToolIndex(ev.ToolCallID, ev.Index)
		tc := map[string]any{"index": idx, "function": map[string]any{"arguments": ev.ArgumentsDelta}}
		return chunk(map[string]any{"tool_calls": []any{tc}}, nil, nil), nil
	case StreamFinish:
		return chunk(map[string]any{}, mapFinishReason(ev.FinishReason, ChatCompletions, false), nil), nil
	case StreamUsage:
		u := ev.Usage
		return chunk(map[string]any{}, nil, map[string]any{"prompt_tokens": u.InputTokens, "completion_tokens": u.OutputTokens, "total_tokens": u.TotalTokens}), nil
	case StreamError:
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": ev.Error, "type": "upstream_stream_error"}})
		return []byte("data: " + string(b) + "\n\n"), nil
	case StreamDone:
		return []byte("data: [DONE]\n\n"), nil
	}
	return nil, nil
}

func (e *StreamEncoder) anthIndex(key string) int {
	if idx, ok := e.anthropicBlocks[key]; ok {
		return idx
	}
	idx := e.anthropicNext
	e.anthropicNext++
	e.anthropicBlocks[key] = idx
	return idx
}
func sseEvent(name string, v any) []byte {
	b, _ := json.Marshal(v)
	return []byte("event: " + name + "\ndata: " + string(b) + "\n\n")
}
func (e *StreamEncoder) closeAnthropicBlocks() []byte {
	var buf bytes.Buffer
	for idx := 0; idx < e.anthropicNext; idx++ {
		if e.anthropicOpen[idx] {
			buf.Write(sseEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx}))
			e.anthropicOpen[idx] = false
		}
	}
	return buf.Bytes()
}
func (e *StreamEncoder) encodeMessages(ev CanonicalStreamEvent) ([]byte, error) {
	switch ev.Type {
	case StreamStart:
		if e.started {
			return nil, nil
		}
		e.started = true
		return sseEvent("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fallbackID(e.id, "msg_omnibridge"), "type": "message", "role": "assistant", "model": e.model, "content": []any{}, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}}), nil
	case StreamTextDelta:
		idx := e.anthIndex("text")
		var buf bytes.Buffer
		if !e.anthropicOpen[idx] {
			buf.Write(sseEvent("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": ""}}))
			e.anthropicOpen[idx] = true
		}
		buf.Write(sseEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": ev.TextDelta}}))
		return buf.Bytes(), nil
	case StreamReasoningDelta:
		idx := e.anthIndex("reasoning")
		var buf bytes.Buffer
		if !e.anthropicOpen[idx] {
			buf.Write(sseEvent("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "thinking", "thinking": ""}}))
			e.anthropicOpen[idx] = true
		}
		buf.Write(sseEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "thinking_delta", "thinking": ev.Reasoning}}))
		return buf.Bytes(), nil
	case StreamToolStart:
		key := "tool:" + fallbackID(ev.ToolCallID, fmt.Sprintf("%d", ev.Index))
		idx := e.anthIndex(key)
		e.anthropicOpen[idx] = true
		return sseEvent("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": fallbackID(ev.ToolCallID, fmt.Sprintf("toolu_omnibridge_%d", idx)), "name": ev.ToolName, "input": map[string]any{}}}), nil
	case StreamToolDelta:
		key := "tool:" + fallbackID(ev.ToolCallID, fmt.Sprintf("%d", ev.Index))
		idx := e.anthIndex(key)
		return sseEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": ev.ArgumentsDelta}}), nil
	case StreamFinish:
		var buf bytes.Buffer
		buf.Write(e.closeAnthropicBlocks())
		buf.Write(sseEvent("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": mapFinishReason(ev.FinishReason, Messages, false)}, "usage": map[string]any{"output_tokens": e.usage.OutputTokens}}))
		return buf.Bytes(), nil
	case StreamUsage:
		// Anthropic's stream protocol reports input on message_start and output on
		// message_delta. Emit a legal message_delta usage update when available.
		return sseEvent("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{}, "usage": map[string]any{"output_tokens": ev.Usage.OutputTokens}}), nil
	case StreamError:
		return sseEvent("error", map[string]any{"type": "error", "error": map[string]any{"type": "upstream_stream_error", "message": ev.Error}}), nil
	case StreamDone:
		var buf bytes.Buffer
		buf.Write(e.closeAnthropicBlocks())
		buf.Write(sseEvent("message_stop", map[string]any{"type": "message_stop"}))
		return buf.Bytes(), nil
	}
	return nil, nil
}

func (e *StreamEncoder) responseIndex(key string) int {
	if idx, ok := e.responseItems[key]; ok {
		return idx
	}
	idx := e.responseNext
	e.responseNext++
	e.responseItems[key] = idx
	return idx
}

func (e *StreamEncoder) encodeResponses(ev CanonicalStreamEvent) ([]byte, error) {
	responseID := fallbackID(e.id, "resp_omnibridge")
	switch ev.Type {
	case StreamStart:
		if e.started {
			return nil, nil
		}
		e.started = true
		return sseEvent("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "object": "response", "status": "in_progress", "model": e.model, "output": []any{}}}), nil
	case StreamTextDelta:
		idx := e.responseIndex("text")
		var buf bytes.Buffer
		if !e.responseTextStarted {
			e.responseTextStarted = true
			buf.Write(sseEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "response_id": responseID, "output_index": idx, "item": map[string]any{"id": fmt.Sprintf("msg_omnibridge_%d", idx), "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}))
			buf.Write(sseEvent("response.content_part.added", map[string]any{"type": "response.content_part.added", "response_id": responseID, "output_index": idx, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}))
		}
		buf.Write(sseEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "response_id": responseID, "output_index": idx, "content_index": 0, "delta": ev.TextDelta}))
		return buf.Bytes(), nil
	case StreamReasoningDelta:
		idx := e.responseIndex("reasoning")
		var buf bytes.Buffer
		if !e.responseReasoningStarted {
			e.responseReasoningStarted = true
			buf.Write(sseEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "response_id": responseID, "output_index": idx, "item": map[string]any{"id": fmt.Sprintf("rs_omnibridge_%d", idx), "type": "reasoning", "status": "in_progress", "summary": []any{}}}))
			buf.Write(sseEvent("response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "response_id": responseID, "output_index": idx, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}))
		}
		buf.Write(sseEvent("response.reasoning_summary_text.delta", map[string]any{"type": "response.reasoning_summary_text.delta", "response_id": responseID, "output_index": idx, "summary_index": 0, "delta": ev.Reasoning}))
		return buf.Bytes(), nil
	case StreamToolStart:
		key := "tool:" + fallbackID(ev.ToolCallID, fmt.Sprintf("%d", ev.Index))
		idx := e.responseIndex(key)
		return sseEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "response_id": responseID, "output_index": idx, "item": map[string]any{"type": "function_call", "id": fallbackID(ev.ToolCallID, fmt.Sprintf("fc_omnibridge_%d", idx)), "call_id": fallbackID(ev.ToolCallID, fmt.Sprintf("call_omnibridge_%d", idx)), "name": ev.ToolName, "arguments": ""}}), nil
	case StreamToolDelta:
		key := "tool:" + fallbackID(ev.ToolCallID, fmt.Sprintf("%d", ev.Index))
		idx := e.responseIndex(key)
		return sseEvent("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "response_id": responseID, "output_index": idx, "delta": ev.ArgumentsDelta}), nil
	case StreamFinish:
		return nil, nil
	case StreamUsage:
		return nil, nil
	case StreamError:
		return sseEvent("response.failed", map[string]any{"type": "response.failed", "response": map[string]any{"id": responseID, "status": "failed", "error": map[string]any{"message": ev.Error}}}), nil
	case StreamDone:
		var buf bytes.Buffer
		if e.responseTextStarted {
			idx := e.responseIndex("text")
			buf.Write(sseEvent("response.output_text.done", map[string]any{"type": "response.output_text.done", "response_id": responseID, "output_index": idx, "content_index": 0, "text": ""}))
			buf.Write(sseEvent("response.content_part.done", map[string]any{"type": "response.content_part.done", "response_id": responseID, "output_index": idx, "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}}))
			buf.Write(sseEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "response_id": responseID, "output_index": idx, "item": map[string]any{"id": fmt.Sprintf("msg_omnibridge_%d", idx), "type": "message", "status": "completed", "role": "assistant", "content": []any{}}}))
		}
		u := e.usage
		buf.Write(sseEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "object": "response", "status": "completed", "model": e.model, "output": []any{}, "usage": map[string]any{"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "total_tokens": u.TotalTokens}}}))
		return buf.Bytes(), nil
	}
	return nil, nil
}

func mergeUsage(a, b CanonicalUsage) CanonicalUsage {
	if b.InputTokens != 0 {
		a.InputTokens = b.InputTokens
	}
	if b.OutputTokens != 0 {
		a.OutputTokens = b.OutputTokens
	}
	if b.TotalTokens != 0 {
		a.TotalTokens = b.TotalTokens
	} else {
		a.TotalTokens = a.InputTokens + a.OutputTokens
	}
	return a
}

// TranslateSSEStream performs live cross-protocol translation with synchronous
// read/write backpressure. Returning from write cancels the upstream loop at the
// server layer through the request context.
func TranslateSSEStream(from, to string, src io.Reader, write func([]byte) error, observe func(CanonicalStreamEvent)) error {
	dec := NewStreamDecoder(from)
	enc := NewStreamEncoder(to)
	doneSeen := false
	err := ReadSSE(src, func(msg SSEMessage) error {
		events, err := dec.DecodeSSE(msg)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if observe != nil {
				observe(ev)
			}
			b, err := enc.EncodeSSE(ev)
			if err != nil {
				return err
			}
			if len(b) > 0 {
				if err := write(b); err != nil {
					return err
				}
			}
			if ev.Type == StreamDone {
				doneSeen = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !doneSeen {
		ev := CanonicalStreamEvent{Type: StreamDone, Done: true, ID: dec.id, Model: dec.model}
		if observe != nil {
			observe(ev)
		}
		b, e := enc.EncodeSSE(ev)
		if e != nil {
			return e
		}
		if len(b) > 0 {
			return write(b)
		}
	}
	return nil
}
