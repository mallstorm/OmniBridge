package protocol

// Canonical* types are the protocol-neutral representation used by OmniBridge
// when translating between client and upstream API dialects. Public aliases at
// the bottom preserve the pre-3.0 package API for existing callers/tests.
type CanonicalPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

type CanonicalToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CanonicalMessage struct {
	Role               string              `json:"role"`
	Parts              []CanonicalPart     `json:"parts,omitempty"`
	ToolCallID         string              `json:"tool_call_id,omitempty"`
	ToolCalls          []CanonicalToolCall `json:"tool_calls,omitempty"`
	Reasoning          string              `json:"reasoning,omitempty"`
	ReasoningSignature string              `json:"reasoning_signature,omitempty"`
}

type CanonicalTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type CanonicalRequest struct {
	Model          string             `json:"model"`
	System         string             `json:"system,omitempty"`
	Messages       []CanonicalMessage `json:"messages,omitempty"`
	Tools          []CanonicalTool    `json:"tools,omitempty"`
	ToolChoice     any                `json:"tool_choice,omitempty"`
	Stream         bool               `json:"stream,omitempty"`
	MaxTokens      int                `json:"max_tokens,omitempty"`
	Temperature    any                `json:"temperature,omitempty"`
	ResponseFormat any                `json:"response_format,omitempty"`
	Reasoning      any                `json:"reasoning,omitempty"`
	ParallelTools  *bool              `json:"parallel_tools,omitempty"`
}

type CanonicalUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type CanonicalResponse struct {
	ID           string              `json:"id"`
	Model        string              `json:"model"`
	Text         string              `json:"text,omitempty"`
	ToolCalls    []CanonicalToolCall `json:"tool_calls,omitempty"`
	Reasoning    string              `json:"reasoning,omitempty"`
	FinishReason string              `json:"finish_reason,omitempty"`
	InputTokens  int64               `json:"input_tokens,omitempty"`
	OutputTokens int64               `json:"output_tokens,omitempty"`
	TotalTokens  int64               `json:"total_tokens,omitempty"`
}

// CanonicalStreamEvent is the transport-neutral stream envelope used by Stage 9.
// Stage 8 defines and validates the contract; the next stage wires live SSE/WS
// decoders and encoders to it.
type CanonicalStreamEvent struct {
	Type           string              `json:"type"`
	ID             string              `json:"id,omitempty"`
	Model          string              `json:"model,omitempty"`
	TextDelta      string              `json:"text_delta,omitempty"`
	Reasoning      string              `json:"reasoning_delta,omitempty"`
	ToolCalls      []CanonicalToolCall `json:"tool_calls,omitempty"`
	Index          int                 `json:"index,omitempty"`
	ToolCallID     string              `json:"tool_call_id,omitempty"`
	ToolName       string              `json:"tool_name,omitempty"`
	ArgumentsDelta string              `json:"arguments_delta,omitempty"`
	FinishReason   string              `json:"finish_reason,omitempty"`
	Usage          CanonicalUsage      `json:"usage,omitempty"`
	Error          string              `json:"error,omitempty"`
	Done           bool                `json:"done,omitempty"`
}

type NormalizationReport struct {
	ToolIDsGenerated       int      `json:"tool_ids_generated"`
	ArgumentsRepaired      int      `json:"arguments_repaired"`
	MissingToolResults     int      `json:"missing_tool_results"`
	OrphanToolResults      int      `json:"orphan_tool_results"`
	SchemasCoerced         int      `json:"schemas_coerced"`
	SystemMessagesHoisted  int      `json:"system_messages_hoisted"`
	ReasoningNormalized    int      `json:"reasoning_normalized"`
	UnknownFieldsPreserved bool     `json:"unknown_fields_preserved"`
	Warnings               []string `json:"warnings,omitempty"`
}

// Compatibility aliases for code written against Protocol Engine 2.x.
type Part = CanonicalPart
type ToolCall = CanonicalToolCall
type Message = CanonicalMessage
type Tool = CanonicalTool
type Request = CanonicalRequest
type Response = CanonicalResponse
