package server

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"omnibridge/internal/observability"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
	"omnibridge/internal/resilience"
	obr "omnibridge/internal/router"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
const wsMaxMessage = 32 << 20

type wsConn struct {
	conn net.Conn
	rw   *bufio.ReadWriter
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") || !headerToken(r.Header.Get("Connection"), "upgrade") {
		return nil, errors.New("websocket upgrade required")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		return nil, errors.New("unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("invalid Sec-WebSocket-Key")
	}
	h := sha1.Sum([]byte(key + wsMagicGUID))
	accept := base64.StdEncoding.EncodeToString(h[:])
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("HTTP server does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	protocolHeader := ""
	if headerToken(r.Header.Get("Sec-WebSocket-Protocol"), "omnibridge.stream.v1") {
		protocolHeader = "Sec-WebSocket-Protocol: omnibridge.stream.v1\r\n"
	}
	if _, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", accept, protocolHeader); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, rw: rw}, nil
}

func headerToken(v, token string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

func (c *wsConn) close() { _ = c.conn.Close() }
func (c *wsConn) writeFrame(op byte, payload []byte) error {
	if len(payload) > wsMaxMessage {
		return errors.New("websocket frame too large")
	}
	h := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		h = append(h, byte(n))
	case n <= 0xffff:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		h = append(h, 127, 0, 0, 0, 0, byte(uint64(n)>>24), byte(uint64(n)>>16), byte(uint64(n)>>8), byte(n))
	}
	if _, err := c.rw.Write(h); err != nil {
		return err
	}
	if _, err := c.rw.Write(payload); err != nil {
		return err
	}
	return c.rw.Flush()
}
func (c *wsConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, b)
}
func (c *wsConn) writeClose(code uint16, reason string) error {
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p, code)
	copy(p[2:], reason)
	return c.writeFrame(0x8, p)
}

func (c *wsConn) readText() ([]byte, error) {
	var assembled []byte
	for {
		b1, err := c.rw.ReadByte()
		if err != nil {
			return nil, err
		}
		b2, err := c.rw.ReadByte()
		if err != nil {
			return nil, err
		}
		fin := b1&0x80 != 0
		op := b1 & 0x0f
		masked := b2&0x80 != 0
		n := uint64(b2 & 0x7f)
		if n == 126 {
			var x uint16
			if err := binary.Read(c.rw, binary.BigEndian, &x); err != nil {
				return nil, err
			}
			n = uint64(x)
		}
		if n == 127 {
			if err := binary.Read(c.rw, binary.BigEndian, &n); err != nil {
				return nil, err
			}
		}
		if n > wsMaxMessage {
			return nil, errors.New("websocket message too large")
		}
		if !masked {
			return nil, errors.New("client websocket frames must be masked")
		}
		mask := make([]byte, 4)
		if _, err := io.ReadFull(c.rw, mask); err != nil {
			return nil, err
		}
		p := make([]byte, int(n))
		if _, err := io.ReadFull(c.rw, p); err != nil {
			return nil, err
		}
		for i := range p {
			p[i] ^= mask[i%4]
		}
		switch op {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, p); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1, 0x0:
			assembled = append(assembled, p...)
			if len(assembled) > wsMaxMessage {
				return nil, errors.New("websocket message too large")
			}
			if fin {
				return assembled, nil
			}
		default:
			return nil, fmt.Errorf("unsupported websocket opcode %d", op)
		}
	}
}

type wsRequestEnvelope struct {
	Type    string          `json:"type,omitempty"`
	APIType string          `json:"api_type,omitempty"`
	Request json.RawMessage `json:"request,omitempty"`
}

// websocket is OmniBridge Stream Transport v1. The client sends one request
// frame and receives protocol-neutral CanonicalStreamEvent JSON frames. This
// keeps the transport stable while the HTTP endpoints continue exposing native
// OpenAI/Responses/Anthropic SSE dialects.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	release, ok := acquireSlot(r.Context(), s.generationSlots, 2*time.Second)
	if !ok {
		busy(w, "websocket_generation")
		return
	}
	defer release()
	c, err := upgradeWebSocket(w, r)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer c.close()
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Minute))
	raw, err := c.readText()
	if err != nil {
		return
	}
	apiType, body, err := parseWSRequest(raw, r.URL.Query().Get("api_type"))
	if err != nil {
		_ = c.writeJSON(protocol.CanonicalStreamEvent{Type: protocol.StreamError, Error: err.Error()})
		_ = c.writeClose(1007, "invalid request")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if err := s.proxyWebSocket(ctx, c, apiType, body); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		_ = c.writeJSON(protocol.CanonicalStreamEvent{Type: protocol.StreamError, Error: err.Error()})
		_ = c.writeClose(1011, "upstream stream failed")
		return
	}
	_ = c.writeClose(1000, "complete")
}

func parseWSRequest(raw []byte, queryType string) (string, []byte, error) {
	var env wsRequestEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", nil, errors.New("first WebSocket frame must be JSON")
	}
	apiType := strings.TrimSpace(env.APIType)
	if apiType == "" {
		apiType = strings.TrimSpace(queryType)
	}
	if apiType == "" {
		apiType = protocol.ChatCompletions
	}
	if apiType != protocol.ChatCompletions && apiType != protocol.Responses && apiType != protocol.Messages {
		return "", nil, fmt.Errorf("unsupported api_type %q", apiType)
	}
	body := env.Request
	if len(body) == 0 || string(body) == "null" {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return "", nil, err
		}
		delete(m, "type")
		delete(m, "api_type")
		delete(m, "request")
		body, _ = json.Marshal(m)
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil || strings.TrimSpace(textFromMap(m, "model")) == "" {
		return "", nil, errors.New("WebSocket request must contain model")
	}
	m["stream"] = true
	body, _ = json.Marshal(m)
	return apiType, body, nil
}
func textFromMap(m map[string]any, key string) string { x, _ := m[key].(string); return x }

func (s *Server) proxyWebSocket(ctx context.Context, c *wsConn, apiType string, body []byte) error {
	s.refreshResilienceConfig()
	start := time.Now()
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &meta) != nil || meta.Model == "" {
		return errors.New("model required")
	}
	annotateTrace(ctx, func(tr *observability.Trace) { tr.Model = meta.Model; tr.Streaming = true })
	if s.QuotaCost != nil {
		s.QuotaCost.UpdateConfig(s.Config.Get().QuotaCost)
		if reject, reason := s.QuotaCost.ShouldRejectForBudget(); reject {
			annotateTrace(ctx, func(tr *observability.Trace) { tr.ErrorKind = reason })
			return errors.New("OmniBridge budget limit reached: " + reason)
		}
	}
	body, contextReport := s.applyContextGuard(body, apiType, meta.Model)
	annotateTrace(ctx, func(tr *observability.Trace) { tr.ContextAction = contextReport.Action })
	sessionKey := extractRoutingSession(&http.Request{Header: http.Header{}}, body)
	req := obr.AnalyzeRequest(body, apiType)
	req.Streaming = true
	candidates, err := s.routingCandidatesForRequest(meta.Model, sessionKey, req)
	if err != nil {
		return err
	}
	routing := s.Config.Get().Routing
	maxAttempts := routing.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	attemptModels := append([]providers.Model(nil), candidates...)
	if len(attemptModels) > maxAttempts {
		attemptModels = attemptModels[:maxAttempts]
	}
	if len(attemptModels) == 1 {
		for len(attemptModels) < maxAttempts {
			attemptModels = append(attemptModels, attemptModels[0])
		}
	}
	var m providers.Model
	var call upstreamCall
	attempted := false
	for attempt, candidate := range attemptModels {
		if !s.acquireModelResilience(candidate) {
			continue
		}
		attempted = true
		m = candidate
		annotateTrace(ctx, func(tr *observability.Trace) {
			tr.SelectedModel = m.ID
			tr.Provider = m.ProviderID
			tr.Attempts = attempt + 1
		})
		call, err = s.upstream(ctx, m, apiType, body)
		if s.QuotaCost != nil && call.Header != nil {
			s.QuotaCost.ObserveHeaders(m.ProviderID, call.Header)
		}
		annotateTrace(ctx, func(tr *observability.Trace) { tr.CrossProtocol = call.CrossProtocol })
		errorBody := captureUpstreamErrorBody(&call)
		class := resilience.Classify(call.Status, errorBody, call.Header, err, ctx.Err())
		if class.Kind != "" {
			s.recordResilienceFailure(m, class)
		}
		if err == nil && call.Status/100 == 2 {
			s.recordResilienceSuccess(m)
			if s.Router != nil && sessionKey != "" && routing.SessionStickiness {
				s.Router.Pin(meta.Model, sessionKey, m.ID, time.Duration(routing.SessionTTLSeconds)*time.Second)
			}
			break
		}
		if !fallbackEligible(class) || attempt == len(attemptModels)-1 {
			break
		}
		if call.Body != nil {
			call.Body.Close()
			call.Body = nil
		}
	}
	if !attempted {
		return errors.New("all routing candidates are blocked by resilience policy")
	}
	if err != nil {
		return err
	}
	if call.Body == nil {
		return errors.New("empty upstream response")
	}
	defer call.Body.Close()
	if call.Status/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(call.Body, 1<<20))
		return fmt.Errorf("upstream status %d: %s", call.Status, strings.TrimSpace(string(raw)))
	}
	var usage protocol.CanonicalUsage
	writeEvent := func(ev protocol.CanonicalStreamEvent) error {
		if ev.Type == protocol.StreamUsage {
			if ev.Usage.InputTokens != 0 {
				usage.InputTokens = ev.Usage.InputTokens
			}
			if ev.Usage.OutputTokens != 0 {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
			if ev.Usage.TotalTokens != 0 {
				usage.TotalTokens = ev.Usage.TotalTokens
			}
		}
		return c.writeJSON(ev)
	}
	isSSE := strings.Contains(strings.ToLower(call.Header.Get("Content-Type")), "text/event-stream")
	if isSSE {
		dec := protocol.NewStreamDecoder(call.TargetProtocol)
		err = protocol.ReadSSE(call.Body, func(msg protocol.SSEMessage) error {
			events, e := dec.DecodeSSE(msg)
			if e != nil {
				return e
			}
			for _, ev := range events {
				if e = writeEvent(ev); e != nil {
					return e
				}
			}
			return nil
		})
	} else {
		raw, _ := io.ReadAll(io.LimitReader(call.Body, 32<<20))
		translated, e := protocol.TranslateResponse(call.TargetProtocol, apiType, raw)
		if e != nil {
			return e
		}
		res, e := protocol.ParseResponse(apiType, translated)
		if e != nil {
			return e
		}
		events := responseToStreamEvents(res)
		for _, ev := range events {
			if e = writeEvent(ev); e != nil {
				return e
			}
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	s.recordUsageCost(ctx, m, err != nil && ctx.Err() == nil, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, time.Since(start).Milliseconds())
	return err
}

func responseToStreamEvents(r protocol.CanonicalResponse) []protocol.CanonicalStreamEvent {
	out := []protocol.CanonicalStreamEvent{{Type: protocol.StreamStart, ID: r.ID, Model: r.Model}}
	if r.Reasoning != "" {
		out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamReasoningDelta, ID: r.ID, Model: r.Model, Reasoning: r.Reasoning})
	}
	if r.Text != "" {
		out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamTextDelta, ID: r.ID, Model: r.Model, TextDelta: r.Text})
	}
	for i, tc := range r.ToolCalls {
		out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamToolStart, ID: r.ID, Model: r.Model, Index: i, ToolCallID: tc.ID, ToolName: tc.Name}, protocol.CanonicalStreamEvent{Type: protocol.StreamToolDelta, ID: r.ID, Model: r.Model, Index: i, ToolCallID: tc.ID, ToolName: tc.Name, ArgumentsDelta: tc.Arguments})
	}
	out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamFinish, ID: r.ID, Model: r.Model, FinishReason: r.FinishReason})
	if r.InputTokens != 0 || r.OutputTokens != 0 || r.TotalTokens != 0 {
		out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamUsage, ID: r.ID, Model: r.Model, Usage: protocol.CanonicalUsage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, TotalTokens: r.TotalTokens}})
	}
	out = append(out, protocol.CanonicalStreamEvent{Type: protocol.StreamDone, ID: r.ID, Model: r.Model, Done: true})
	return out
}
