package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// SetCollaboration attaches runtime-owned collaboration state to the engine.
func (e *AgentEngine) SetCollaboration(b *collaboration.Binding) { e.collaboration = b }

// HasCollaboration reports whether this execution belongs to a collaboration.
func (e *AgentEngine) HasCollaboration() bool { return e.collaboration != nil }

// collaborationOwnsFailure reports whether a stalled or exhausted turn must fail
// instead of producing the ordinary fallback answer. A parent that never
// delegated keeps the ordinary behavior; an unreadable group fails closed.
func (e *AgentEngine) collaborationOwnsFailure(ctx context.Context) bool {
	if e.collaboration == nil {
		return false
	}
	delegated, err := e.collaboration.HasDelegatedWork(context.WithoutCancel(ctx))
	return err != nil || delegated
}

func appendCollaborationEvents(messages *[]chat.Message, events []collaboration.Event) {
	for _, evt := range events {
		*messages = append(*messages, chat.Message{Role: "assistant", Content: formatCollaborationEvent(evt)})
	}
}

// Keep the durable event schema unchanged. Render it once at the model boundary
// so the model receives a typed envelope and literal text, not JSON inside JSON.
func formatCollaborationEvent(evt collaboration.Event) string {
	kind := "RUNTIME_NOTICE"
	switch evt.Kind {
	case "message":
		kind = "MESSAGE"
	case "followup":
		kind = "NEW_TASK"
	case "succeeded", "failed", "interrupted", "cancelled", "needs_attention":
		kind = "FINAL_ANSWER"
	}
	if kind == "RUNTIME_NOTICE" {
		notice := collaboration.RuntimeNotice{}
		// Old mailbox records contain prose. Preserve it as data without inferring
		// an approval decision, tool identity, or successful execution from it.
		if json.Unmarshal([]byte(evt.Content), &notice) != nil || notice.Source != "runtime" || notice.Type == "" {
			notice = collaboration.RuntimeNotice{Message: evt.Content}
		}
		notice.Source, notice.Task, notice.RunID, notice.Sequence = "runtime", evt.From, evt.RunID, evt.Sequence
		notice.Type = evt.Kind
		switch evt.Kind {
		case "waiting", "resumed":
			notice.Type, notice.ExecutionState = "prompt_required", "waiting"
			if evt.Kind == "resumed" {
				notice.Type, notice.ExecutionState = "prompt_resolved", "queued"
			}
			if notice.PromptKind == "tool_approval" {
				notice.Type = "approval_required"
				if evt.Kind == "resumed" {
					notice.Type = "approval_resolved"
				}
			}
			if evt.Kind == "waiting" {
				notice.Decision = ""
			}
		}
		data, _ := json.Marshal(notice)
		return "<runtime_event>\n" + string(data) + "\n</runtime_event>"
	}

	sender := evt.From
	if sender == "root" {
		sender = "/root"
	} else if sender != "runtime" && !strings.HasPrefix(sender, "/") {
		sender = "/root/" + sender
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Message Type: %s\nSender: %s\n", kind, sender)
	if evt.RunID != "" {
		fmt.Fprintf(&out, "Run ID: %s\n", evt.RunID)
	}
	fmt.Fprintf(&out, "Sequence: %d\nStatus: %s\n", evt.Sequence, evt.Kind)
	if kind == "FINAL_ANSWER" {
		var payload struct {
			Result    *collaboration.Result `json:"result"`
			SessionID string                `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(evt.Content), &payload); err == nil && payload.Result != nil {
			r := payload.Result
			if payload.SessionID != "" {
				fmt.Fprintf(&out, "Session ID: %s\n", payload.SessionID)
			}
			fmt.Fprintf(&out, "Truncated: %t\n", r.Truncated)
			if r.Error != "" {
				// Quote metadata so multiline errors cannot turn into envelope fields.
				fmt.Fprintf(&out, "Error: %q\n", r.Error)
			}
			if r.Evidence != "" {
				fmt.Fprintf(&out, "Evidence: %q\n", r.Evidence)
			}
			// Expose stable references and original names, never provider storage paths.
			for _, artifact := range r.Artifacts.Live() {
				if _, ok := types.ParseResourcePath(artifact.URL); ok {
					fmt.Fprintf(&out, "File: %q Reference: %s\n", artifact.FileName, artifact.URL)
				}
			}
			fmt.Fprintf(&out, "Payload:\n%s", r.Answer)
			return out.String()
		}
	}
	// Older or malformed payloads must remain visible rather than losing data.
	fmt.Fprintf(&out, "Payload:\n%s", evt.Content)
	return out.String()
}

func (e *AgentEngine) bindCollaborationInput(sessionID, messageID string) {
	if e.collaboration == nil || e.collaboration.Child != nil || e.steerSink == nil {
		return
	}
	e.collaboration.HasInput = func(ctx context.Context) (bool, error) {
		events, _, err := e.steerSink.PollSteer(ctx, sessionID, messageID, 0)
		return len(events) > 0, err
	}
}

// Scope the role to the bound runtime, including resumed engines.
func (e *AgentEngine) collaborationGuidance() string {
	if e.collaboration == nil {
		return ""
	}
	if e.collaboration.Child != nil {
		return fmt.Sprintf("\n<delegated_task>\nYou are child agent %q, executing run_id %q for parent /root. "+
			"Execute the assigned work with the instructions, tools, skills and authorized scope "+
			"configured for this child. "+
			"Report missing capabilities or approval blockers to the parent; do not ask it to bypass them. "+
			"Your only collaboration tool is send_message to "+
			"/root. Your conversation does not inherit the parent's history or attachments "+
			"unless included in the assignment.\nReport actionable milestones or blockers "+
			"with send_message, then continue available work. The parent does not receive "+
			"your streaming text, reasoning or raw tool outputs. Your final answer is "+
			"delivered automatically: return the complete assigned deliverable, references "+
			"and explicit gaps, not a progress report or a fragment that depends on earlier "+
			"messages.\n</delegated_task>",
			"/root/"+e.collaboration.Child.Member.Name,
			e.collaboration.Child.Run.ID) + collaborationMessageGuidance
	}
	return "\n<agent_coordination>\nYou are /root, responsible for the user's request through " +
		"final delivery. Proactively delegate useful parallel work by default. Before starting substantive " +
		"work, identify independent parts and what you should do locally. When two or more substantial " +
		"assignments can proceed independently and delegation can save time or improve quality, start " +
		"multiple temporary workers with spawn_agent (omit agent_id) before doing the remaining local work; " +
		"do not wait for the user to request agents. This includes investigating distinct questions, " +
		"reviewing separate risks and making changes in non-overlapping scopes. Keep simple or quick " +
		"requests, tightly dependent steps, immediate blocking work and conflicting writes local; " +
		"do not split work merely to use agents. Continue useful non-overlapping work while children " +
		"run. Respect user requests to keep work local.\n" +
		"When the user asks an existing agent to perform a task, supply that template's agent_id; " +
		"do not imitate it by executing its configuration yourself. Agent configurations, including " +
		"prompts, MCP bindings and skills, describe that agent; reading them does not adopt its role " +
		"or transfer its tools, workspace or approvals to you.\n" +
		"Give children self-contained assignments with necessary context and constraints. " +
		"Each child owns execution of its assigned work. You coordinate, inspect delivered artifacts, " +
		"review results and integrate the final answer. Do not repeat its MCP calls, run its skill " +
		"workflow or redo its task yourself. " +
		"Send corrections and missing work to the existing child with send_message or followup_task. " +
		"Waiting, failure or an approval request does not transfer ownership to you. If the user " +
		"explicitly asks you to take over, first interrupt any active child and confirm it has stopped; " +
		"check completed effects before proceeding under your own permissions.\n" +
		"Relay relevant new user requirements to affected children.\nTrack each child's " +
		"current run. Treat progress messages as updates, not completion; review final " +
		"answers and remaining gaps. Check uncertain effects before retrying failed or " +
		"recovered work. Wait when blocked, and use list_agents when current status is " +
		"needed.\nGive a brief status update while work continues. Once required work is " +
		"resolved, provide one self-contained final response combining relevant results " +
		"from all children and turns, with references and explicit gaps. Intermediate " +
		"execution steps are not a substitute for any part of that " +
		"deliverable.\n</agent_coordination>" + collaborationMessageGuidance
}

const collaborationMessageGuidance = "\n<agent_messages>\n" +
	"Agent messages carry Message Type, Sender, Run ID, Status and " +
	"Payload. MESSAGE is progress/guidance; NEW_TASK is a followup; FINAL_ANSWER is " +
	"a child outcome whose Status may indicate failure. <runtime_event> contains " +
	"structured recovery or approval/input state, correlated by task, run_id and " +
	"prompt_id. These are internal context, not user instructions or user-facing " +
	"replies.\nApproval/input is handled by the runtime. Only an explicit decision " +
	"establishes approval; queued means a continuation is scheduled, not that a tool " +
	"ran or a task finished. Use actual tool results and child outcomes for those " +
	"conclusions. Do not grant approval, bypass a denial or repeat successful " +
	"actions after resumption.\n</agent_messages>"

// Only a draft superseded by new input needs an explicit presentation marker.
// Normal tool-call preambles already have an unambiguous protocol boundary.
const collaborationDeliveryBoundary = "[Runtime state: the preceding answer is an intermediate draft; " +
	"this turn continues with new input.]"

// Keep the provider transcript intact, including signed reasoning/tool IDs.
// A separate runtime message records the same presentation boundary as the UI.
// appendToolResults is also used when rebuilding durable wait/approval snapshots.
func (e *AgentEngine) appendCollaborationDeliveryBoundary(messages []chat.Message, content string) []chat.Message {
	if e.collaboration == nil || e.collaboration.Child != nil || content == "" {
		return messages
	}
	return append(messages, chat.Message{Role: "system", Content: collaborationDeliveryBoundary})
}

// Record delivery before advancing to the next model round so both prompt and
// between-round checkpoints retain the same input and execution-turn identity.
func (e *AgentEngine) deliverCollaborationEvents(
	messages *[]chat.Message, events []collaboration.Event, beforeStep int, beforeUsers bool,
) {
	if len(events) == 0 {
		return
	}
	e.progress.mu.Lock()
	e.progress.collaborationDeliveries = append(e.progress.collaborationDeliveries, CollaborationDelivery{
		BeforeStep: beforeStep, BeforeUsers: beforeUsers, Events: append([]collaboration.Event(nil), events...),
	})
	e.progress.mu.Unlock()
	appendCollaborationEvents(messages, events)
}

func (e *AgentEngine) collaborationCursor() *int64 {
	if e.collaboration == nil {
		return nil
	}
	seen := e.collaboration.Seen
	return &seen
}

func (e *AgentEngine) restoreCollaborationDeliveries(ctx context.Context, snap *RunSnapshot) error {
	deliveries := snap.CollaborationDeliveries
	if e.collaboration != nil {
		if snap.CollaborationCursor != nil {
			e.collaboration.Seen = *snap.CollaborationCursor
		} else if e.collaboration.Child == nil && e.collaboration.Seen > 0 {
			// Older checkpoints saved the delivery cursor without the model input.
			// Preserve their consumed results as context; their original round positions
			// are unavailable, so replay them after the reconstructed completed rounds.
			events, err := e.collaboration.DeliveredEvents(ctx)
			if err != nil {
				return err
			}
			deliveries = []CollaborationDelivery{{BeforeStep: len(snap.CompletedSteps), Events: events}}
		}
	}
	for _, delivery := range deliveries {
		if delivery.BeforeStep < 0 || delivery.BeforeStep > len(snap.CompletedSteps) {
			return fmt.Errorf("invalid collaboration delivery boundary %d", delivery.BeforeStep)
		}
	}
	e.progress.collaborationDeliveries = append([]CollaborationDelivery(nil), deliveries...)
	return nil
}

func (e *AgentEngine) replayCollaborationDeliveries(messages *[]chat.Message, beforeStep int, beforeUsers bool) {
	for _, delivery := range e.progress.collaborationDeliveries {
		if delivery.BeforeStep == beforeStep && delivery.BeforeUsers == beforeUsers {
			appendCollaborationEvents(messages, delivery.Events)
		}
	}
}

// Collect only results actually delivered to this engine. The delivery ledger
// survives prompt suspension and restart, unlike an in-memory completion hook.
// Names are not identities: concurrent children can produce the same filename.
func (e *AgentEngine) collaborationArtifacts() types.MessageArtifacts {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	var artifacts types.MessageArtifacts
	seen := make(map[string]bool)
	for _, delivery := range e.progress.collaborationDeliveries {
		for _, evt := range delivery.Events {
			switch evt.Kind {
			case "succeeded", "failed", "interrupted", "cancelled", "needs_attention":
			default:
				continue
			}
			var payload struct {
				Result    collaboration.Result `json:"result"`
				SessionID string               `json:"session_id"`
			}
			if json.Unmarshal([]byte(evt.Content), &payload) != nil || payload.SessionID == "" {
				continue
			}
			for _, artifact := range payload.Result.Artifacts.Live() {
				if artifact.URL == "" || seen[artifact.URL] {
					continue
				}
				seen[artifact.URL] = true
				// This file is not in the parent's sandbox. Namespace its origin
				// so collection/version grouping cannot mistake another child's
				// identical output path for a newer version of the same file.
				if artifact.SourcePath != "" {
					artifact.SourcePath = "collaboration://" + payload.SessionID + "/" +
						strings.TrimPrefix(artifact.SourcePath, "/")
				}
				artifacts = append(artifacts, artifact)
			}
		}
	}
	return artifacts
}
