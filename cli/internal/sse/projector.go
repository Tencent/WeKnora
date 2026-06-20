package sse

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/cli/internal/format"
	sdk "github.com/Tencent/WeKnora/client"
)

// ProjectedEvent is the bounded event representation shared by
// buffered JSON, human-readable text, and MCP tool results. It deliberately
// mirrors the SDK field names so consumers can move between projected JSON and
// raw NDJSON without learning a second event vocabulary.
type ProjectedEvent struct {
	ID                  string                  `json:"id,omitempty"`
	ResponseType        string                  `json:"response_type"`
	Content             string                  `json:"content,omitempty"`
	Done                bool                    `json:"done"`
	KnowledgeReferences []format.ReferenceIndex `json:"knowledge_references,omitempty"`
	SessionID           string                  `json:"session_id,omitempty"`
	AssistantMessageID  string                  `json:"assistant_message_id,omitempty"`
	ToolCalls           []sdk.LLMToolCall       `json:"tool_calls,omitempty"`
	Data                map[string]any          `json:"data,omitempty"`
}

// Projector observes every SDK frame for lifecycle state, then returns the
// presentation event selected by the output contract. Default output keeps
// answer frames only. Reference enables bounded citation events; verbose
// enables execution and lifecycle events.
type Projector struct {
	verbose            bool
	reference          bool
	fallbackKBID       string
	seen               bool
	done               bool
	sessionID          string
	assistantMessageID string
}

func NewProjector(verbose, reference bool, fallbackKBID string) *Projector {
	return &Projector{verbose: verbose, reference: reference, fallbackKBID: fallbackKBID}
}

func (p *Projector) Seen() bool                 { return p.seen }
func (p *Projector) Done() bool                 { return p.done }
func (p *Projector) SessionID() string          { return p.sessionID }
func (p *Projector) AssistantMessageID() string { return p.assistantMessageID }

// Chat consumes one KnowledgeQAStream frame.
func (p *Projector) Chat(r *sdk.StreamResponse) (ProjectedEvent, bool) {
	if r == nil || p.done {
		return ProjectedEvent{}, false
	}
	p.seen = true
	if p.sessionID == "" && r.SessionID != "" {
		p.sessionID = r.SessionID
	}
	if p.assistantMessageID == "" && r.AssistantMessageID != "" {
		p.assistantMessageID = r.AssistantMessageID
	}
	if r.ResponseType == sdk.ResponseTypeComplete {
		p.done = true
	}

	responseType := string(r.ResponseType)
	isAnswer := r.ResponseType == sdk.ResponseTypeAnswer || (r.ResponseType == "" && r.Content != "")
	isReference := r.ResponseType == sdk.ResponseTypeReferences
	include := isAnswer || (p.verbose && !isReference) || (p.reference && isReference)
	if !include && p.reference && len(r.KnowledgeReferences) > 0 {
		return p.chatReferenceEvent(r), true
	}
	if !include {
		return ProjectedEvent{}, false
	}
	if responseType == "" {
		responseType = string(sdk.ResponseTypeAnswer)
	}
	event := ProjectedEvent{
		ID:                 r.ID,
		ResponseType:       responseType,
		Content:            r.Content,
		Done:               r.Done,
		SessionID:          r.SessionID,
		AssistantMessageID: r.AssistantMessageID,
		ToolCalls:          r.ToolCalls,
		Data:               r.Data,
	}
	if p.reference {
		event.KnowledgeReferences = format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID)
	}
	return event, true
}

// Agent consumes one AgentQAStream frame.
func (p *Projector) Agent(r *sdk.AgentStreamResponse) (ProjectedEvent, bool) {
	if r == nil || p.done {
		return ProjectedEvent{}, false
	}
	p.seen = true
	if r.ResponseType == sdk.AgentResponseTypeComplete {
		p.done = true
	}

	isAnswer := r.ResponseType == sdk.AgentResponseTypeAnswer
	isReference := r.ResponseType == sdk.AgentResponseTypeReferences
	include := isAnswer || (p.verbose && !isReference) || (p.reference && isReference)
	if !include && p.reference && len(r.KnowledgeReferences) > 0 {
		return p.agentReferenceEvent(r), true
	}
	if !include {
		return ProjectedEvent{}, false
	}
	event := ProjectedEvent{
		ID:           r.ID,
		ResponseType: string(r.ResponseType),
		Content:      r.Content,
		Done:         r.Done,
		Data:         r.Data,
	}
	if p.reference {
		event.KnowledgeReferences = format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID)
	}
	return event, true
}

func (p *Projector) chatReferenceEvent(r *sdk.StreamResponse) ProjectedEvent {
	return ProjectedEvent{
		ID:                  r.ID,
		ResponseType:        string(sdk.ResponseTypeReferences),
		Done:                r.Done,
		KnowledgeReferences: format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID),
		SessionID:           r.SessionID,
		AssistantMessageID:  r.AssistantMessageID,
	}
}

func (p *Projector) agentReferenceEvent(r *sdk.AgentStreamResponse) ProjectedEvent {
	return ProjectedEvent{
		ID:                  r.ID,
		ResponseType:        string(sdk.AgentResponseTypeReferences),
		Done:                r.Done,
		KnowledgeReferences: format.IndexReferences(r.KnowledgeReferences, p.fallbackKBID),
	}
}

// TextRenderer writes projected events as a human-readable stream.
// TTY detection is intentionally absent: terminals and pipes receive the same
// filtering semantics; callers may add styling later without changing them.
type TextRenderer struct {
	w       io.Writer
	verbose bool
	lastKey string
	atBOL   bool
}

func NewTextRenderer(w io.Writer, verbose bool) *TextRenderer {
	return &TextRenderer{w: w, verbose: verbose, atBOL: true}
}

func (r *TextRenderer) Write(ev ProjectedEvent) error {
	switch ev.ResponseType {
	case string(sdk.ResponseTypeAnswer):
		return r.writeContentBlock(ev, "answer")
	case string(sdk.ResponseTypeThinking), string(sdk.ResponseTypeReflection):
		return r.writeContentBlock(ev, ev.ResponseType)
	case string(sdk.ResponseTypeReferences):
		return r.writeReferences(ev)
	default:
		return r.writeStructured(ev)
	}
}

func (r *TextRenderer) Close() error {
	if !r.atBOL {
		return r.writeString("\n")
	}
	return nil
}

func (r *TextRenderer) writeContentBlock(ev ProjectedEvent, label string) error {
	key := ev.ResponseType + "\x00" + ev.ID
	if key != r.lastKey {
		if err := r.ensureBOL(); err != nil {
			return err
		}
		if r.verbose {
			if err := r.writeString("[" + label + "]\n"); err != nil {
				return err
			}
		}
		r.lastKey = key
	}
	if ev.Content != "" {
		if err := r.writeString(ev.Content); err != nil {
			return err
		}
	}
	if ev.Done {
		r.lastKey = ""
		return r.ensureBOL()
	}
	return nil
}

func (r *TextRenderer) writeReferences(ev ProjectedEvent) error {
	if err := r.ensureBOL(); err != nil {
		return err
	}
	if err := r.writeString("[references]\n"); err != nil {
		return err
	}
	for _, ref := range ev.KnowledgeReferences {
		line := "- chunk_id=" + ref.ChunkID
		if ref.KBID != "" {
			line += " kb_id=" + ref.KBID
		}
		if ref.ParentChunkID != "" {
			line += " parent_chunk_id=" + ref.ParentChunkID
		}
		if err := r.writeString(line + "\n"); err != nil {
			return err
		}
	}
	r.lastKey = ""
	return nil
}

func (r *TextRenderer) writeStructured(ev ProjectedEvent) error {
	if err := r.ensureBOL(); err != nil {
		return err
	}
	header := "[" + ev.ResponseType
	if ev.ID != "" {
		header += " " + ev.ID
	}
	header += "]"
	if err := r.writeString(header); err != nil {
		return err
	}
	if ev.Content != "" {
		if err := r.writeString(" " + strings.ReplaceAll(ev.Content, "\n", " ")); err != nil {
			return err
		}
	}
	if len(ev.ToolCalls) > 0 {
		b, _ := json.Marshal(ev.ToolCalls)
		if err := r.writeString(" " + string(b)); err != nil {
			return err
		}
	}
	if len(ev.Data) > 0 {
		b, _ := json.Marshal(ev.Data)
		if err := r.writeString(" " + string(b)); err != nil {
			return err
		}
	}
	if err := r.writeString("\n"); err != nil {
		return err
	}
	r.lastKey = ""
	return nil
}

func (r *TextRenderer) ensureBOL() error {
	if r.atBOL {
		return nil
	}
	return r.writeString("\n")
}

func (r *TextRenderer) writeString(s string) error {
	if s == "" {
		return nil
	}
	if _, err := fmt.Fprint(r.w, s); err != nil {
		return err
	}
	r.atBOL = strings.HasSuffix(s, "\n")
	return nil
}
