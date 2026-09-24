package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The OpenAI chat API, as a client sends it, translated into the Anthropic
// Messages API and back — tools and images included, so an agent that speaks
// only OpenAI can use Claude through the gateway.

// defaultMaxTokens stands in when the client names none: Anthropic requires
// one, and an agent's answer is rarely short.
const defaultMaxTokens = 4096

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"`
	Tools               []chatTool      `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	User                string          `json:"user,omitempty"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []chatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type messagesRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	System        string            `json:"system,omitempty"`
	Messages      []messageTurn     `json:"messages"`
	Stream        bool              `json:"stream,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Tools         []messagesTool    `json:"tools,omitempty"`
	ToolChoice    map[string]any    `json:"tool_choice,omitempty"`
	Metadata      *messagesMetadata `json:"metadata,omitempty"`
}

type messagesMetadata struct {
	UserID string `json:"user_id"`
}

type messageTurn struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

type messagesTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// badRequest is a request the translation cannot carry: the client hears
// about it as a 400, not as a model that ignored half of what it was sent.
type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

// toMessages translates a chat completions body into a Messages body.
func toMessages(body []byte) (messagesRequest, error) {
	var in chatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return messagesRequest{}, badRequest{"invalid JSON: " + err.Error()}
	}
	out := messagesRequest{
		Model:       in.Model,
		MaxTokens:   defaultMaxTokens,
		Stream:      in.Stream,
		Temperature: in.Temperature,
		TopP:        in.TopP,
	}
	if in.MaxCompletionTokens > 0 {
		out.MaxTokens = in.MaxCompletionTokens
	} else if in.MaxTokens > 0 {
		out.MaxTokens = in.MaxTokens
	}
	if in.User != "" {
		out.Metadata = &messagesMetadata{UserID: in.User}
	}

	stops, err := stopSequences(in.Stop)
	if err != nil {
		return out, err
	}
	out.StopSequences = stops

	var system []string
	for i, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			text, err := plainText(m.Content)
			if err != nil {
				return out, badRequest{fmt.Sprintf("messages[%d]: %v", i, err)}
			}
			system = append(system, text)
		case "user":
			blocks, err := userBlocks(m.Content)
			if err != nil {
				return out, badRequest{fmt.Sprintf("messages[%d]: %v", i, err)}
			}
			out.Messages = appendTurn(out.Messages, "user", blocks)
		case "assistant":
			blocks, err := assistantBlocks(m)
			if err != nil {
				return out, badRequest{fmt.Sprintf("messages[%d]: %v", i, err)}
			}
			out.Messages = appendTurn(out.Messages, "assistant", blocks)
		case "tool":
			text, err := plainText(m.Content)
			if err != nil {
				return out, badRequest{fmt.Sprintf("messages[%d]: %v", i, err)}
			}
			// Anthropic takes a tool's answer as a block in the user's turn.
			out.Messages = appendTurn(out.Messages, "user", []map[string]any{{
				"type": "tool_result", "tool_use_id": m.ToolCallID, "content": text,
			}})
		default:
			return out, badRequest{fmt.Sprintf("messages[%d]: role %q is not supported for Claude", i, m.Role)}
		}
	}
	out.System = strings.Join(system, "\n\n")

	for _, t := range in.Tools {
		if t.Type != "" && t.Type != "function" {
			return out, badRequest{fmt.Sprintf("tool type %q is not supported for Claude", t.Type)}
		}
		schema := t.Function.Parameters
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Tools = append(out.Tools, messagesTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: schema})
	}
	choice, err := toolChoice(in.ToolChoice)
	if err != nil {
		return out, err
	}
	if in.ParallelToolCalls != nil && !*in.ParallelToolCalls && len(out.Tools) > 0 {
		if choice == nil {
			choice = map[string]any{"type": "auto"}
		}
		if choice["type"] != "none" {
			choice["disable_parallel_tool_use"] = true
		}
	}
	out.ToolChoice = choice
	return out, nil
}

// appendTurn adds blocks to the conversation, joining them to the last turn
// when it is the same role: Anthropic wants user and assistant to alternate,
// and several tool answers in a row are one user turn to it.
func appendTurn(turns []messageTurn, role string, blocks []map[string]any) []messageTurn {
	if len(blocks) == 0 {
		return turns
	}
	if n := len(turns); n > 0 && turns[n-1].Role == role {
		turns[n-1].Content = append(turns[n-1].Content, blocks...)
		return turns
	}
	return append(turns, messageTurn{Role: role, Content: blocks})
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// parts reads content that is either a string or a list of parts.
func parts(raw json.RawMessage) ([]contentPart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []contentPart{{Type: "text", Text: s}}, nil
	}
	var ps []contentPart
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("content is neither a string nor a list of parts")
	}
	return ps, nil
}

// plainText is content that may only be text: system prompts and tool answers.
func plainText(raw json.RawMessage) (string, error) {
	ps, err := parts(raw)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range ps {
		if p.Type != "text" {
			return "", fmt.Errorf("only text is supported here, not %q", p.Type)
		}
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

func userBlocks(raw json.RawMessage) ([]map[string]any, error) {
	ps, err := parts(raw)
	if err != nil {
		return nil, err
	}
	var blocks []map[string]any
	for _, p := range ps {
		switch p.Type {
		case "text":
			if p.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		case "image_url":
			if p.ImageURL == nil {
				return nil, fmt.Errorf("image_url part without a url")
			}
			src, err := imageSource(p.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": src})
		default:
			return nil, fmt.Errorf("content part %q is not supported for Claude", p.Type)
		}
	}
	return blocks, nil
}

// imageSource turns an image URL — a data: URL or a web address — into
// Anthropic's image source.
func imageSource(u string) (map[string]any, error) {
	if rest, ok := strings.CutPrefix(u, "data:"); ok {
		meta, data, found := strings.Cut(rest, ",")
		mediaType, isBase64 := strings.CutSuffix(meta, ";base64")
		if !found || !isBase64 || mediaType == "" {
			return nil, fmt.Errorf("an image data: URL must be base64 with a media type")
		}
		return map[string]any{"type": "base64", "media_type": mediaType, "data": data}, nil
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return map[string]any{"type": "url", "url": u}, nil
	}
	return nil, fmt.Errorf("unsupported image URL")
}

func assistantBlocks(m chatMessage) ([]map[string]any, error) {
	text, err := plainText(m.Content)
	if err != nil {
		return nil, err
	}
	var blocks []map[string]any
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, tc := range m.ToolCalls {
		input := json.RawMessage(`{}`)
		if a := strings.TrimSpace(tc.Function.Arguments); a != "" {
			if !json.Valid([]byte(a)) {
				return nil, fmt.Errorf("tool call %s has arguments that are not JSON", tc.ID)
			}
			input = json.RawMessage(a)
		}
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
	}
	return blocks, nil
}

func stopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, badRequest{"stop must be a string or a list of strings"}
	}
	return many, nil
}

func toolChoice(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var word string
	if json.Unmarshal(raw, &word) == nil {
		switch word {
		case "auto":
			return map[string]any{"type": "auto"}, nil
		case "none":
			return map[string]any{"type": "none"}, nil
		case "required":
			return map[string]any{"type": "any"}, nil
		}
		return nil, badRequest{fmt.Sprintf("tool_choice %q is not supported", word)}
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err != nil || named.Function.Name == "" {
		return nil, badRequest{"tool_choice must be auto, none, required or a named function"}
	}
	return map[string]any{"type": "tool", "name": named.Function.Name}, nil
}

// fromMessages translates a Messages response into a chat completion.
func fromMessages(r anthropicResponse) map[string]any {
	var text strings.Builder
	var calls []map[string]any
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id": b.ID, "type": "function",
				"function": map[string]any{"name": b.Name, "arguments": args},
			})
		}
	}
	msg := map[string]any{"role": "assistant", "content": text.String()}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		if text.Len() == 0 {
			msg["content"] = nil
		}
	}
	return map[string]any{
		"id":     r.ID,
		"object": "chat.completion",
		"model":  r.Model,
		"choices": []map[string]any{{
			"index": 0, "message": msg, "finish_reason": finishReason(r.StopReason),
		}},
		"usage": openAIUsage(r.Usage),
	}
}

// openAIError puts an Anthropic error body in the shape an OpenAI client
// parses; anything else comes through as it was.
func openAIError(body []byte) []byte {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error.Message == "" {
		return body
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": e.Error.Message, "type": e.Error.Type}})
	return b
}
