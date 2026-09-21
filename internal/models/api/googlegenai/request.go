// Package googlegenai implements the native Google Gemini generateContent
// wire protocol (https://ai.google.dev/api/generate-content). Every
// model-specific deviation is driven by catalog.GoogleGenerativeAISettings;
// this package contains no vendor names beyond the protocol itself.
package googlegenai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint api.Endpoint
	Settings catalog.GoogleGenerativeAISettings
	// ThinkingLevels maps neutral levels to the vendor vocabulary
	// (thinkingLevel values for Gemini 3, "off" support for budget models).
	ThinkingLevels api.ThinkingLevelMap
	// Reasoning marks a reasoning model. The Gemini protocol has no
	// role selection to gate on it; the field is informational and mirrors
	// the other protocol configs.
	Reasoning bool
}

const (
	roleUser  = "user"
	roleModel = "model"

	// metadataThoughtSignature is the ToolCallMetadata key under which a
	// function call's thoughtSignature round-trips. Gemini 3 rejects a
	// replayed function call whose signature is missing or altered.
	metadataThoughtSignature = "thought_signature"

	defaultImageMIME = "image/jpeg"
)

type part = map[string]any

// convertMessages splits neutral messages into the system instruction text
// and the Gemini contents array. Consecutive tool results are merged into one
// user content carrying several functionResponse parts, as Gemini requires
// every functionCall of a model turn to be answered in the next content.
func (c *Client) convertMessages(messages []api.Message) (system []string, contents []map[string]any) {
	toolNames := map[string]string{}
	openToolBatch := -1 // index in contents of the functionResponse content being filled
	for _, msg := range messages {
		msg = api.NeutralizeMessageSpecialTokens(msg)
		switch msg.Role {
		case "system":
			if text := systemText(msg); text != "" {
				system = append(system, text)
			}
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolNames[tc.ID] = tc.Function.Name
				}
			}
			contents = append(contents, map[string]any{"role": roleModel, "parts": assistantParts(msg)})
		case "tool":
			p := toolResultPart(msg, toolNames)
			if openToolBatch >= 0 && openToolBatch == len(contents)-1 {
				batch := contents[openToolBatch]
				batch["parts"] = append(batch["parts"].([]part), p)
				continue
			}
			contents = append(contents, map[string]any{"role": roleUser, "parts": []part{p}})
			openToolBatch = len(contents) - 1
		default:
			contents = append(contents, map[string]any{"role": roleUser, "parts": userParts(msg)})
		}
	}
	return system, contents
}

func systemText(msg api.Message) string {
	if len(msg.MultiContent) == 0 {
		return msg.Content
	}
	var texts []string
	for _, p := range msg.MultiContent {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// userParts renders a user turn. Legacy Message.Images are emitted before the
// text part, matching the Chat Completions package.
func userParts(msg api.Message) []part {
	var parts []part
	switch {
	case len(msg.MultiContent) > 0:
		for _, p := range msg.MultiContent {
			switch p.Type {
			case "text":
				if p.Text != "" {
					parts = append(parts, part{"text": p.Text})
				}
			case "image_url":
				if p.ImageURL != nil {
					if ip := imagePart(p.ImageURL.URL); ip != nil {
						parts = append(parts, ip)
					}
				}
			}
		}
	case len(msg.Images) > 0:
		for _, img := range msg.Images {
			if ip := imagePart(img); ip != nil {
				parts = append(parts, ip)
			}
		}
		parts = append(parts, part{"text": msg.Content})
	}
	if len(parts) == 0 {
		parts = append(parts, part{"text": msg.Content})
	}
	return parts
}

// assistantParts renders a model turn: the answer text (carrying the thought
// signature when the turn had no function calls) followed by one functionCall
// part per tool call, each replaying its own thought signature. Reasoning
// text is never replayed as a thought part: Gemini only needs signatures.
func assistantParts(msg api.Message) []part {
	var parts []part
	if msg.Content != "" || len(msg.ToolCalls) == 0 {
		p := part{"text": msg.Content}
		sig := api.SignatureFor(api.APIGoogleGenerativeAI, msg.ReasoningSignature)
		if sig != "" && len(msg.ToolCalls) == 0 {
			p["thoughtSignature"] = sig
		}
		parts = append(parts, p)
	}
	for _, tc := range msg.ToolCalls {
		fc := map[string]any{
			"name": tc.Function.Name,
			"args": parseArgs(tc.Function.Arguments),
		}
		if tc.ID != "" {
			fc["id"] = tc.ID
		}
		p := part{"functionCall": fc}
		if sig := thoughtSignatureFromMetadata(tc.ProviderMetadata); sig != "" {
			p["thoughtSignature"] = sig
		}
		parts = append(parts, p)
	}
	return parts
}

// toolResultPart renders a tool result as a functionResponse part. The tool
// name comes from the message or, failing that, from the assistant turn that
// issued the call.
func toolResultPart(msg api.Message, toolNames map[string]string) part {
	name := msg.Name
	if name == "" {
		name = toolNames[msg.ToolCallID]
	}
	fr := map[string]any{
		"name":     name,
		"response": map[string]any{"result": decodeToolResult(msg.Content)},
	}
	if msg.ToolCallID != "" {
		fr["id"] = msg.ToolCallID
	}
	return part{"functionResponse": fr}
}

// decodeToolResult returns the tool output as a JSON value when it is a JSON
// object or array, otherwise the raw string.
func decodeToolResult(content string) any {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var v any
		if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
			return v
		}
	}
	return content
}

// parseArgs decodes a tool call's JSON arguments into the object Gemini
// expects; anything that is not a JSON object becomes an empty object.
func parseArgs(arguments string) map[string]any {
	args := map[string]any{}
	if strings.TrimSpace(arguments) == "" {
		return args
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args == nil {
		return map[string]any{}
	}
	return args
}

func thoughtSignatureFromMetadata(md map[string]json.RawMessage) string {
	raw, ok := md[metadataThoughtSignature]
	if !ok || len(raw) == 0 {
		return ""
	}
	var sig string
	if err := json.Unmarshal(raw, &sig); err != nil {
		return ""
	}
	return sig
}

// imagePart renders an image reference. Stored images are resolved to data
// URIs first; data URIs become inlineData, everything else is passed as
// fileData with a MIME type guessed from the extension.
func imagePart(ref string) part {
	resolved := api.ResolveImageURLForLLM(ref)
	if strings.HasPrefix(resolved, "data:") {
		mimeType, data, ok := parseDataURI(resolved)
		if !ok {
			return nil
		}
		return part{"inlineData": map[string]any{"mimeType": mimeType, "data": data}}
	}
	return part{"fileData": map[string]any{"mimeType": guessImageMIME(resolved), "fileUri": resolved}}
}

// parseDataURI splits "data:<mime>[;base64],<payload>" into the MIME type and
// the base64 payload Gemini wants. Non-base64 payloads are re-encoded.
func parseDataURI(uri string) (mimeType, data string, ok bool) {
	comma := strings.Index(uri, ",")
	if comma < 0 {
		return "", "", false
	}
	header := uri[len("data:"):comma]
	payload := uri[comma+1:]
	isBase64 := false
	if strings.HasSuffix(header, ";base64") {
		header = strings.TrimSuffix(header, ";base64")
		isBase64 = true
	}
	mimeType = header
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = mimeType[:idx]
	}
	if mimeType == "" {
		mimeType = defaultImageMIME
	}
	if isBase64 {
		return mimeType, payload, true
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		decoded = payload
	}
	return mimeType, base64.StdEncoding.EncodeToString([]byte(decoded)), true
}

func guessImageMIME(ref string) string {
	ext := path.Ext(ref)
	if u, err := url.Parse(ref); err == nil && u.Path != "" {
		ext = path.Ext(u.Path)
	}
	if ext != "" {
		if mt := mime.TypeByExtension(strings.ToLower(ext)); strings.HasPrefix(mt, "image/") {
			return mt
		}
	}
	return defaultImageMIME
}

// buildBody assembles the generateContent request body. The body is the
// same for streaming and non-streaming calls; streaming is selected by the
// URL. It returns nested maps so golden tests can inspect it.
func (c *Client) buildBody(messages []api.Message, opts *api.Options) (map[string]any, error) {
	s := c.cfg.Settings
	system, contents := c.convertMessages(messages)
	if contents == nil {
		// "contents" is required and must be an array: a nil slice marshals to
		// null and generateContent answers 400. A request carrying only system
		// messages legitimately produces no turns.
		contents = []map[string]any{}
	}
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = map[string]any{
			"parts": []part{{"text": strings.Join(system, "\n\n")}},
		}
	}

	gen := map[string]any{}
	if opts != nil {
		c.applySampling(gen, opts)
		if budget := opts.CompletionBudget(); budget > 0 {
			gen["maxOutputTokens"] = budget
		}
		c.applyTools(body, opts)
		if len(opts.Format) > 0 {
			gen["responseMimeType"] = "application/json"
			appendSchemaHint(contents, opts.Format)
		}
		if tc := c.thinkingConfig(opts); tc != nil {
			gen["thinkingConfig"] = tc
		}
	}
	for k, v := range s.ExtraGeneration {
		if _, exists := gen[k]; !exists {
			gen[k] = v
		}
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	return body, nil
}

func (c *Client) applySampling(gen map[string]any, opts *api.Options) {
	s := c.cfg.Settings
	if opts.Temperature > 0 {
		gen["temperature"] = opts.Temperature
	}
	if opts.TopP > 0 {
		gen["topP"] = opts.TopP
	}
	if opts.Seed > 0 && s.SupportsSeed {
		gen["seed"] = opts.Seed
	}
	if s.SupportsPenalty {
		if opts.FrequencyPenalty > 0 {
			gen["frequencyPenalty"] = opts.FrequencyPenalty
		}
		if opts.PresencePenalty > 0 {
			gen["presencePenalty"] = opts.PresencePenalty
		}
	}
}

func (c *Client) applyTools(body map[string]any, opts *api.Options) {
	if len(opts.Tools) == 0 {
		return
	}
	decls := make([]map[string]any, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		decl := map[string]any{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}
		if schema := sanitizeParameters(tool.Function.Parameters); schema != nil {
			decl["parameters"] = schema
		}
		decls = append(decls, decl)
	}
	body["tools"] = []map[string]any{{"functionDeclarations": decls}}

	if opts.ToolChoice == "" {
		return
	}
	cfg := map[string]any{}
	switch opts.ToolChoice {
	case "auto":
		cfg["mode"] = "AUTO"
	case "required":
		cfg["mode"] = "ANY"
	case "none":
		cfg["mode"] = "NONE"
	default:
		cfg["mode"] = "ANY"
		cfg["allowedFunctionNames"] = []string{opts.ToolChoice}
	}
	body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
}

// unsupportedSchemaKeys are JSON Schema members the Gemini Schema type
// rejects. They are stripped recursively; property names are never touched.
var unsupportedSchemaKeys = map[string]bool{
	"$schema":              true,
	"$id":                  true,
	"$ref":                 true,
	"$defs":                true,
	"definitions":          true,
	"additionalProperties": true,
	"default":              true,
	"examples":             true,
	"title":                true,
}

// sanitizeParameters decodes a tool's JSON schema and strips the members
// Gemini does not accept. Empty or undecodable schemas yield nil.
func sanitizeParameters(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	if _, ok := schema.(map[string]any); !ok {
		return nil
	}
	return sanitizeSchema(schema)
}

func sanitizeSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if unsupportedSchemaKeys[k] {
				continue
			}
			if k == "properties" {
				if props, ok := val.(map[string]any); ok {
					cleaned := make(map[string]any, len(props))
					for name, sub := range props {
						cleaned[name] = sanitizeSchema(sub)
					}
					out[k] = cleaned
					continue
				}
			}
			out[k] = sanitizeSchema(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = sanitizeSchema(t[i])
		}
		return t
	}
	return v
}

// appendSchemaHint appends the JSON schema instruction to the last user text
// part, mirroring the Chat Completions package.
func appendSchemaHint(contents []map[string]any, format json.RawMessage) {
	hint := fmt.Sprintf("\nUse this JSON schema: %s", format)
	for i := len(contents) - 1; i >= 0; i-- {
		if contents[i]["role"] != roleUser {
			continue
		}
		parts, _ := contents[i]["parts"].([]part)
		for j := len(parts) - 1; j >= 0; j-- {
			if text, ok := parts[j]["text"].(string); ok {
				parts[j]["text"] = text + hint
				return
			}
		}
		contents[i]["parts"] = append(parts, part{"text": strings.TrimPrefix(hint, "\n")})
		return
	}
}

// thinkingConfig encodes the requested reasoning level in the shape the
// model generation supports: thinkingBudget (Gemini 2.5) or thinkingLevel
// (Gemini 3). nil means "send no thinkingConfig".
func (c *Client) thinkingConfig(opts *api.Options) map[string]any {
	s := c.cfg.Settings
	levels := c.cfg.ThinkingLevels
	level, requested := opts.Reasoning()
	// Models the catalog does not mark as reasoning (gemini-2.0-flash, Gemma,
	// unknown ids) may reject thinkingConfig outright, so send none.
	if !requested || !c.cfg.Reasoning {
		return nil
	}
	mode := s.ThinkingMode
	if mode == "" {
		mode = catalog.GoogleThinkingBudget
	}
	switch mode {
	case catalog.GoogleThinkingBudget:
		switch level {
		case api.ReasoningOff:
			if levels.Supports(api.ReasoningOff) {
				return map[string]any{"thinkingBudget": 0}
			}
			return nil
		case api.ReasoningAuto:
			return map[string]any{"thinkingBudget": -1, "includeThoughts": s.IncludeThoughts}
		default:
			level = levels.Clamp(level)
			budget := opts.ThinkingBudgetTokens
			if budget <= 0 {
				if b, ok := s.ThinkingBudgets[level]; ok && level.Graded() {
					budget = b
				} else {
					budget = -1
				}
			}
			return map[string]any{"thinkingBudget": budget, "includeThoughts": s.IncludeThoughts}
		}
	case catalog.GoogleThinkingLevel:
		switch level {
		case api.ReasoningOff:
			// Gemini 3 cannot switch thinking off; only send a level when the
			// vendor explicitly mapped "off" to a wire value.
			if v, ok := levels[api.ReasoningOff]; ok && v != nil && *v != "" {
				return map[string]any{"thinkingLevel": *v}
			}
			return nil
		case api.ReasoningAuto:
			return map[string]any{"includeThoughts": s.IncludeThoughts}
		default:
			level = levels.Clamp(level)
			if !level.Graded() {
				return map[string]any{"includeThoughts": s.IncludeThoughts}
			}
			return map[string]any{"thinkingLevel": levels.Value(level), "includeThoughts": s.IncludeThoughts}
		}
	}
	return nil
}

// BuildRequestBody is the golden-test entry point: it returns the exact
// JSON object that would be sent for the given inputs. The stream flag is
// accepted for interface parity; Gemini selects streaming through the URL.
func (c *Client) BuildRequestBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error) {
	_ = stream
	return c.buildBody(messages, opts)
}
