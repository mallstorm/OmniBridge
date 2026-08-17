package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestLiveAnthropicToChatStreaming(t *testing.T) {
	upstream := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":2}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	var writes [][]byte
	var seen []CanonicalStreamEvent
	err := TranslateSSEStream(Messages, ChatCompletions, strings.NewReader(upstream), func(b []byte) error {
		writes = append(writes, append([]byte(nil), b...))
		return nil
	}, func(ev CanonicalStreamEvent) { seen = append(seen, ev) })
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) < 5 {
		t.Fatalf("expected incremental writes, got %d", len(writes))
	}
	joined := string(bytes.Join(writes, nil))
	if !strings.Contains(joined, `"content":"hel"`) || !strings.Contains(joined, `"content":"lo"`) || !strings.Contains(joined, "[DONE]") {
		t.Fatalf("translated stream=%s", joined)
	}
	var in, out int64
	for _, ev := range seen {
		if ev.Type == StreamUsage {
			if ev.Usage.InputTokens != 0 {
				in = ev.Usage.InputTokens
			}
			if ev.Usage.OutputTokens != 0 {
				out = ev.Usage.OutputTokens
			}
		}
	}
	if in != 2 || out != 3 {
		t.Fatalf("usage in=%d out=%d events=%+v", in, out, seen)
	}
}

func TestChatToolDeltasToAnthropic(t *testing.T) {
	upstream := "data: {\"id\":\"c1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n" +
		"data: {\"id\":\"c1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Berlin\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	var buf bytes.Buffer
	if err := TranslateSSEStream(ChatCompletions, Messages, strings.NewReader(upstream), func(b []byte) error { _, e := buf.Write(b); return e }, nil); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, `"type":"tool_use"`) || !strings.Contains(got, `"type":"input_json_delta"`) || !strings.Contains(got, "message_stop") {
		t.Fatalf("stream=%s", got)
	}
}

func TestResponsesTextDeltaToChat(t *testing.T) {
	upstream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-x\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"token\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-x\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	var buf bytes.Buffer
	if err := TranslateSSEStream(Responses, ChatCompletions, strings.NewReader(upstream), func(b []byte) error { _, e := buf.Write(b); return e }, nil); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, `"content":"token"`) || !strings.Contains(got, "[DONE]") {
		t.Fatalf("stream=%s", got)
	}
}
