package protocol

import (
	"fmt"
	"sort"
	"sync"
)

type adapter struct {
	name           string
	parseRequest   func([]byte) (Request, error)
	encodeRequest  func(Request) ([]byte, error)
	parseResponse  func([]byte) (Response, error)
	encodeResponse func(Response) ([]byte, error)
}

var translatorRegistry = struct {
	sync.RWMutex
	items map[string]adapter
}{items: map[string]adapter{}}

func init() {
	for _, protocol := range []string{ChatCompletions, Messages, Responses} {
		p := protocol
		registerAdapter(p, adapter{
			name: p,
			parseRequest: func(body []byte) (Request, error) {
				return parseRequestBuiltin(p, body)
			},
			encodeRequest: func(req Request) ([]byte, error) {
				return encodeRequestBuiltin(p, req)
			},
			parseResponse: func(body []byte) (Response, error) {
				return parseResponseBuiltin(p, body)
			},
			encodeResponse: func(res Response) ([]byte, error) {
				return encodeResponseBuiltin(p, res)
			},
		})
	}
}

func registerAdapter(protocol string, a adapter) {
	translatorRegistry.Lock()
	defer translatorRegistry.Unlock()
	translatorRegistry.items[protocol] = a
}

func lookupAdapter(protocol string) (adapter, bool) {
	translatorRegistry.RLock()
	defer translatorRegistry.RUnlock()
	a, ok := translatorRegistry.items[protocol]
	return a, ok
}

func RegisteredProtocols() []string {
	translatorRegistry.RLock()
	defer translatorRegistry.RUnlock()
	out := make([]string, 0, len(translatorRegistry.items))
	for k := range translatorRegistry.items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Supports(apiType string) bool {
	_, ok := lookupAdapter(apiType)
	return ok
}

func CanTranslate(from, to string) bool { return Supports(from) && Supports(to) }

func ParseRequest(apiType string, body []byte) (Request, error) {
	a, ok := lookupAdapter(apiType)
	if !ok {
		return Request{}, fmt.Errorf("unsupported request protocol %q", apiType)
	}
	return a.parseRequest(body)
}

func EncodeRequest(apiType string, req Request) ([]byte, error) {
	a, ok := lookupAdapter(apiType)
	if !ok {
		return nil, fmt.Errorf("unsupported request protocol %q", apiType)
	}
	return a.encodeRequest(req)
}

func ParseResponse(apiType string, body []byte) (Response, error) {
	a, ok := lookupAdapter(apiType)
	if !ok {
		return Response{}, fmt.Errorf("unsupported response protocol %q", apiType)
	}
	return a.parseResponse(body)
}

func EncodeResponse(apiType string, res Response) ([]byte, error) {
	a, ok := lookupAdapter(apiType)
	if !ok {
		return nil, fmt.Errorf("unsupported response protocol %q", apiType)
	}
	return a.encodeResponse(res)
}

type EngineStatus struct {
	Version                    string   `json:"version"`
	CanonicalModel             string   `json:"canonical_model"`
	Protocols                  []string `json:"protocols"`
	SameProtocolNormalization  bool     `json:"same_protocol_normalization"`
	ToolSequenceRepair         bool     `json:"tool_sequence_repair"`
	StructuredSchemaCoercion   bool     `json:"structured_schema_coercion"`
	ReasoningReplayFields      bool     `json:"reasoning_replay_fields"`
	CanonicalStreamContract    bool     `json:"canonical_stream_contract"`
	LiveCrossProtocolStreaming bool     `json:"live_cross_protocol_streaming"`
}

func Status() EngineStatus {
	return EngineStatus{
		Version:                    "3.1",
		CanonicalModel:             "CanonicalRequest/Response/StreamEvent",
		Protocols:                  RegisteredProtocols(),
		SameProtocolNormalization:  true,
		ToolSequenceRepair:         true,
		StructuredSchemaCoercion:   true,
		ReasoningReplayFields:      true,
		CanonicalStreamContract:    true,
		LiveCrossProtocolStreaming: true,
	}
}
