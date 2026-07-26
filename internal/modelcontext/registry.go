// Package modelcontext owns every request-local handle exposed to a language
// model. Application code should use Registry instead of coordinating source
// references and durable-resource codecs independently.
//
// Its boundaries are deliberate:
//   - UUIDs, wiki slugs, URLs, and resource:// handles are durable identities.
//   - cN/dN/bN/wN/iN/res://NNNN/ref-N values are temporary model handles.
//   - temporary handles are never persisted or accepted outside their registry.
//   - every model response is decoded before tools, storage, or UI consume it.
package modelcontext

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/Tencent/WeKnora/internal/llmreference"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ArgumentResolutionUnchanged         = "unchanged"
	ArgumentResolutionResolved          = "resolved"
	ArgumentResolutionPartiallyResolved = "partially_resolved"
	ArgumentResolutionUnresolved        = "unresolved"
)

const resourceHandleProtocolPrompt = `

## Resource handle protocol (system-owned)
Some durable resources and high-entropy Wiki slugs are represented by request-local res://NNNN handles. Wiki issues may use iN handles.
- Copy only handles that appeared in supplied context or tool results, preserving them exactly in links, images, and tool arguments.
- Never invent, edit, or expand any handle. The system restores it after generation.`

// ChunkReference describes a retrieved chunk registered in model context.
// It aliases the codec type so callers do not need to import llmreference.
type ChunkReference = llmreference.ChunkReference

// Registry is the single request-scoped boundary between durable application
// identities and temporary model handles.
//
// Durable resource references are encoded before source identifiers. This
// ordering is intentionally private: summary/<knowledge-id> wiki slugs must be
// protected as one resource-like handle before the embedded document ID can be
// compacted to dN. Callers therefore cannot accidentally reverse the codecs.
type Registry struct {
	sources   *llmreference.Registry
	resources *resourceRegistry
	issues    *HandleTable
}

// NewRegistry creates a registry for one model request/Agent execution.
func NewRegistry(citationsEnabled bool) *Registry {
	return &Registry{
		sources:   llmreference.NewRegistry(citationsEnabled),
		resources: newResourceRegistry(),
		issues:    NewHandleTable("i", 0, 1),
	}
}

// ProtocolPrompt returns the system-owned model handle and citation protocol.
func (r *Registry) ProtocolPrompt() string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.ProtocolPrompt() + resourceHandleProtocolPrompt
}

// EncodeMessages returns a model-facing copy with every temporary handle
// encoded in the only safe order.
func (r *Registry) EncodeMessages(messages []chat.Message) []chat.Message {
	if r == nil {
		return messages
	}
	messages = r.resources.EncodeMessages(messages)
	// Register tool-private IDs from all replayed results before encoding any
	// assistant call. The scan is intentionally order-independent, matching the
	// source codec's two-pass replay behavior.
	for i := range messages {
		if messages[i].Role == "tool" {
			messages[i].Content = r.encodeToolPrivateResult(messages[i].Name, messages[i].Content)
		}
	}
	messages = r.sources.EncodeMessagesWithPolicies(messages, sourceArgumentAllowed, sourceOutputAllowed)
	for i := range messages {
		if len(messages[i].ToolCalls) == 0 {
			continue
		}
		messages[i].ToolCalls = append([]chat.ToolCall(nil), messages[i].ToolCalls...)
		for j := range messages[i].ToolCalls {
			r.encodeReplayedToolPolicies(&messages[i].ToolCalls[j])
		}
	}
	return messages
}

// DecodeToolCalls restores all temporary handles in tool-call arguments.
func (r *Registry) DecodeToolCalls(toolCalls []types.LLMToolCall) {
	if r == nil {
		return
	}
	for i := range toolCalls {
		if toolCalls[i].ModelArguments == "" {
			toolCalls[i].ModelArguments = toolCalls[i].Function.Arguments
		}
	}
	r.resources.DecodeToolCalls(toolCalls)
	r.sources.DecodeToolCallsWithPolicy(toolCalls, sourceArgumentAllowed)
	for i := range toolCalls {
		r.decodeToolPolicies(&toolCalls[i])
		resolved := toolCalls[i].Function.Arguments
		unresolved := append(
			r.resources.OrphanAliases(resolved),
			r.sources.UnresolvedToolHandlesWithPolicy(
				toolCalls[i].Function.Name, resolved, sourceArgumentAllowed,
			)...,
		)
		unresolved = append(unresolved, r.unresolvedPrivateToolHandles(toolCalls[i].Function.Name, resolved)...)
		toolCalls[i].UnresolvedHandles = uniqueSorted(unresolved)
		changed := !jsonEquivalent(toolCalls[i].ModelArguments, resolved)
		switch {
		case changed && len(toolCalls[i].UnresolvedHandles) > 0:
			toolCalls[i].ArgumentResolution = ArgumentResolutionPartiallyResolved
		case len(toolCalls[i].UnresolvedHandles) > 0:
			toolCalls[i].ArgumentResolution = ArgumentResolutionUnresolved
		case changed:
			toolCalls[i].ArgumentResolution = ArgumentResolutionResolved
		default:
			toolCalls[i].ArgumentResolution = ArgumentResolutionUnchanged
		}
	}
}

func jsonEquivalent(left, right string) bool {
	var leftValue interface{}
	var rightValue interface{}
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return left == right
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// DecodeResponse restores resources, expands citations, and decodes tool
// arguments in a non-streaming response.
func (r *Registry) DecodeResponse(response *types.ChatResponse) {
	if r == nil || response == nil {
		return
	}
	response.Content = r.DecodeOutputText(response.Content)
	response.ReasoningContent = r.DecodeOutputText(response.ReasoningContent)
	r.DecodeToolCalls(response.ToolCalls)
}

// StreamDecoder creates one ordered decoder for a response text channel.
func (r *Registry) StreamDecoder() *StreamDecoder {
	if r == nil {
		return &StreamDecoder{}
	}
	return &StreamDecoder{
		resources: newResourceStreamDecoder(r.resources),
		sources:   llmreference.NewStreamExpander(r.sources),
		issues:    NewHandleStreamDecoder(r.issues),
		orphans:   &orphanResourceStreamFilter{},
	}
}

// OrphanResourceHandles reports model-generated resource handles with no
// backing durable reference.
func (r *Registry) OrphanResourceHandles(decoded string) []string {
	if r == nil {
		return nil
	}
	return r.resources.OrphanAliases(decoded)
}

func (r *Registry) RegisterChunk(ref ChunkReference) string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.RegisterChunk(ref)
}

func (r *Registry) RegisterDocument(id string) string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.RegisterDocument(id)
}

func (r *Registry) RegisterKnowledgeBase(id string) string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.RegisterKnowledgeBase(id)
}

func (r *Registry) RegisterWeb(rawURL, title string) string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.RegisterWeb(rawURL, title)
}

func (r *Registry) RegisterSearchResults(results []*types.SearchResult) {
	if r == nil || r.sources == nil {
		return
	}
	r.sources.RegisterSearchResults(results)
}

func (r *Registry) ChunkHandle(id string) string {
	if r == nil || r.sources == nil {
		return ""
	}
	return r.sources.ChunkAlias(id)
}

// CompactKnownText replaces only previously registered durable source IDs.
func (r *Registry) CompactKnownText(text string) string {
	if r == nil || r.sources == nil {
		return text
	}
	text = r.resources.EncodeText(text)
	return r.sources.CompactKnownText(text)
}

// ModelToolResult renders a tool result using registered model handles.
func (r *Registry) ModelToolResult(result *types.ToolResult) string {
	return r.ModelToolResultForTool("", result)
}

// ModelToolResultForTool renders a result and applies any explicit private-ID
// policy owned by that built-in tool family.
func (r *Registry) ModelToolResultForTool(toolName string, result *types.ToolResult) string {
	if result == nil {
		return ""
	}
	if r == nil || r.sources == nil {
		if result.Success {
			return result.Output
		}
		return result.Error
	}
	// Protect durable resources and UUID-bearing summary slugs before the
	// source codec sees any embedded document IDs. Encode once more afterwards
	// for resource references rendered from structured ToolResult.Data.
	copyResult := *result
	copyResult.Output = r.encodeToolPrivateResult(toolName, result.Output)
	copyResult.Output = r.resources.EncodeText(copyResult.Output)
	copyResult.Error = r.resources.EncodeText(result.Error)
	var modelOutput string
	if sourceOutputAllowed(toolName) {
		modelOutput = r.sources.ModelOutput(&copyResult)
	} else if copyResult.Success {
		modelOutput = copyResult.Output
	} else if copyResult.Error != "" {
		modelOutput = "Error: " + copyResult.Error
	} else {
		modelOutput = "Error: tool call failed"
	}
	// Even tools without structured source results can surface a known durable
	// ID in validation errors or status text. Compact only explicitly declared
	// built-ins; dynamic MCP output remains fully opaque.
	if sourceCompactionAllowed(toolName) {
		modelOutput = r.sources.CompactKnownText(modelOutput)
	}
	return r.resources.EncodeText(modelOutput)
}

// DecodeOutputText applies the public citation policy to complete text. It is
// primarily used by non-streaming cleanup/fallback paths.
func (r *Registry) DecodeOutputText(text string) string {
	if r == nil {
		return text
	}
	text = r.resources.DecodeText(text)
	text = r.resources.StripOrphanAliases(text)
	text = r.sources.ExpandText(text)
	return r.issues.DecodeKnownText(text)
}

// StreamDecoder composes resource restoration and source-citation expansion
// so callers cannot split handle processing or apply stages in the wrong order.
type StreamDecoder struct {
	resources *resourceStreamDecoder
	sources   *llmreference.StreamExpander
	issues    *HandleStreamDecoder
	orphans   *orphanResourceStreamFilter
}

func (d *StreamDecoder) Feed(chunk string) string {
	if d == nil {
		return chunk
	}
	if d.resources != nil {
		chunk = d.resources.Feed(chunk)
	}
	if d.sources != nil {
		chunk = d.sources.Feed(chunk)
	}
	if d.issues != nil {
		chunk = d.issues.Feed(chunk)
	}
	if d.orphans != nil {
		chunk = d.orphans.Feed(chunk)
	}
	return chunk
}

func (d *StreamDecoder) Flush() string {
	if d == nil {
		return ""
	}
	var tail string
	if d.resources != nil {
		tail = d.resources.Flush()
	}
	if d.sources != nil {
		tail = d.sources.Feed(tail) + d.sources.Flush()
	}
	if d.issues != nil {
		tail = d.issues.Feed(tail) + d.issues.Flush()
	}
	if d.orphans != nil {
		tail = d.orphans.Feed(tail) + d.orphans.Flush()
	}
	return tail
}
