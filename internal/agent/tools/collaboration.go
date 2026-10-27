package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/types"
)

// CollaborationToolNames lists the six runtime collaboration tools.
var CollaborationToolNames = []string{
	"spawn_agent",
	"list_agents",
	"send_message",
	"followup_task",
	"interrupt_agent",
	"wait_agent",
}

// ResolveDelegatedAgent authorizes and snapshots an existing template. It must
// never accept a model-supplied owner, tenant, permission or session identifier.
type ResolveDelegatedAgent func(context.Context, string) (string, error)

// CollaborationTool executes a collaboration command within its runtime binding.
type CollaborationTool struct {
	BaseTool
	binding *collaboration.Binding
	resolve ResolveDelegatedAgent
}

// NewCollaborationTools builds the tool set available to a parent or child execution.
func NewCollaborationTools(b *collaboration.Binding, resolve ResolveDelegatedAgent) []types.Tool {
	defs := []struct {
		name, description, properties string
		required                      []string
	}{
		{
			"spawn_agent",
			"Start a child task asynchronously. Omit agent_id to create a temporary worker with " +
				"the current agent's effective model, instructions, tools and authorized scope. " +
				"Supply agent_id to delegate to an existing same-workspace reasoning agent template " +
				"using that template's model and tools. Neither mode creates a saved agent template. " +
				"For continuation, check list_agents and use followup_task on the existing instance. " +
				"Pass a self-contained assignment: children have separate conversations and sandboxes, " +
				"without parent history or attachments. fork_turns/model/reasoning_effort and nested " +
				"spawning are unsupported. Returns member_id, task_name and agent_path immediately. " +
				"Limits: 16 instances per task, 3 executing concurrently; others queue and approval " +
				"waits release their slots. Messages and final results arrive asynchronously.",
			"\"agent_id\":{\"type\":\"string\",\"description\":\"Optional existing reasoning agent template ID " +
				"in this workspace. Omit to inherit the current agent. Obtain template IDs from the agent " +
				"catalog.\"},\"task_name\":{\"type\":\"string\",\"pattern\":\"^[a-z][a-z0-9_]{0,47}$\",\"desc" +
				"ription\":\"Unique instance name within this task, for example research_docs. " +
				"Address it later as research_docs or /root/research_docs.\"},\"message\":{\"type\":\"s" +
				"tring\",\"description\":\"Self-contained task and required context, maximum 16000 " +
				"UTF-8 bytes.\"}",
			[]string{"task_name", "message"},
		},
		{
			"list_agents",
			"Read child instances in this conversation, including previous-turn instances available for " +
				"followup_task: member_id, task_name, agent_id, session_id, " +
				"run_id, status, progress, prompt_id and cancel_requested. This snapshot does " +
				"not consume messages or contain transcripts/final answers.\nStatuses: queued, " +
				"running, waiting (user approval/input), succeeded, failed, interrupted, " +
				"cancelled, needs_attention (uncertain execution). Progress contains " +
				"phase/tool/iteration. cancel_requested means stopping, not yet stopped. Use " +
				"run_id as expected_run_id for commands tied to this observed turn.",
			"\"path_prefix\":{\"type\":\"string\",\"description\":\"Omit or /root for all children; " +
				"task_name or /root/task_name selects one child. No nested children in this " +
				"version.\"}",
			nil,
		},
		{
			"send_message",
			"Queue information for an existing child without starting an idle turn. Running " +
				"children receive it at the next model boundary after the in-flight call; " +
				"waiting children receive it after prompt resolution. Idle messages remain " +
				"queued until followup_task starts work.\nReturns the addressed run_id. Success " +
				"acknowledges queuing, not processing or completion. expected_run_id rejects " +
				"stale commands. This does not resolve approvals.",
			"\"target\":{\"type\":\"string\",\"description\":\"Child member_id, task_name or " +
				"/root/task_name. A child can target only " +
				"/root.\"},\"message\":{\"type\":\"string\",\"description\":\"Guidance or progress report, " +
				"maximum 16000 UTF-8 bytes.\"},\"expected_run_id\":{\"type\":\"string\",\"description\":\"O" +
				"ptional current run_id from list_agents; rejects stale commands targeting a " +
				"newer turn.\"}",
			[]string{"target", "message"},
		},
		{
			"followup_task",
			"Give an existing child a follow-up task. If idle after " +
				"success/failure/interruption, queue a new turn in the same conversation. If " +
				"queued/running/waiting, send guidance to that turn; an unconsumed followup " +
				"racing completion queues another turn.\nReturns run_id asynchronously. Cannot " +
				"target /root, a stopping child or needs_attention. Limit: 8 turns per child. " +
				"expected_run_id rejects stale commands. Pending approvals remain pending.",
			"\"target\":{\"type\":\"string\",\"description\":\"Existing child member_id, task_name or " +
				"/root/task_name.\"},\"message\":{\"type\":\"string\",\"description\":\"Next task or " +
				"active-task guidance, maximum 16000 UTF-8 " +
				"bytes.\"},\"expected_run_id\":{\"type\":\"string\",\"description\":\"Optional observed " +
				"run_id; reject the followup if a newer turn has started.\"}",
			[]string{"target", "message"},
		},
		{
			"interrupt_agent",
			"Request cancellation of a child's current turn, retaining the instance for " +
				"later followup_task. Queued/waiting turns stop immediately; running tools may " +
				"stop later. Returns an interruption acknowledgement; use list_agents to confirm " +
				"terminal status.\nPending prompts are closed by the runtime; committed effects " +
				"cannot be undone. expected_run_id protects newer turns. Stopping the parent " +
				"cancels the group.",
			"\"target\":{\"type\":\"string\",\"description\":\"Child member_id, task_name or " +
				"/root/task_name to interrupt.\"},\"expected_run_id\":{\"type\":\"string\",\"description\"" +
				":\"Optional observed current run_id, protecting a newer turn from stale " +
				"cancellation.\"}",
			[]string{"target"},
		},
		{
			"wait_agent",
			"Wait on the shared parent mailbox for any child's message/outcome, " +
				"approval/input state transition, user steer, cancellation or timeout. No target " +
				"is required; one update does not mean all children finished.\nReturns " +
				"updated/timed_out/interrupted flags, not message bodies; the runtime injects " +
				"bodies separately. interrupted=true means user input is ready, not a child " +
				"failure. Tool telemetry does not wake the model. Approval/input waiting and " +
				"resumption are delivered once per state transition; approval cards and " +
				"decisions are handled by the runtime.\nDefault 30000 ms, range 1000–60000 ms. " +
				"Prefer longer waits when blocked. Timeout leaves child execution unchanged.",
			"\"timeout_ms\":{\"type\":\"integer\",\"minimum\":1000,\"maximum\":60000,\"description\":\"Max" +
				"imum wait duration in milliseconds, default 30000. Child notifications and user " +
				"input can end it earlier.\"}",
			nil,
		},
	}
	var out []types.Tool
	for _, d := range defs {
		if b.Child != nil && d.name != "send_message" {
			continue
		}
		if b.Child != nil {
			d.description = "Queue a message to your parent assistant at /root. The runtime wakes a pending " +
				"parent wait and delivers it before the next model call. Success acknowledges " +
				"queuing, not a reply or completion; your turn continues. Cancelled or " +
				"superseded runs cannot send messages. This does not resolve approval/input " +
				"requests."
			d.properties = "\"target\":{\"type\":\"string\",\"enum\":[\"/root\"],\"description\":\"Your parent " +
				"assistant. Always /root.\"},\"message\":{\"type\":\"string\",\"description\":\"Progress, " +
				"finding or blocker, maximum 16000 UTF-8 bytes.\"}"
		}
		required, _ := json.Marshal(d.required)
		if d.required == nil {
			required = []byte(`[]`)
		}
		schema := `{"type":"object","properties":{` + d.properties + `},"required":` + string(
			required,
		) + `,"additionalProperties":false}`
		out = append(out, &CollaborationTool{NewBaseTool(d.name, d.description, json.RawMessage(schema)), b, resolve})
	}
	return out
}

// ExecutionTimeout allows waiting on the mailbox beyond normal tool timeouts.
func (t *CollaborationTool) ExecutionTimeout() time.Duration { return 65 * time.Second }

// Execute validates arguments and dispatches the scoped collaboration operation.
func (t *CollaborationTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var in struct {
		AgentID  string `json:"agent_id"`
		Name     string `json:"task_name"`
		Message  string `json:"message"`
		Target   string `json:"target"`
		Expected string `json:"expected_run_id"`
		Prefix   string `json:"path_prefix"`
		Timeout  *int   `json:"timeout_ms"`
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return operationFailure(err.Error()), nil
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return operationFailure("expected one JSON object"), nil
	}
	b := t.binding
	ctx = b.Context(ctx)
	exec, _ := ToolExecFromContext(ctx)
	key := ""
	if exec != nil && exec.ToolCallID != "" {
		key = exec.AssistantMessageID + ":" + exec.ToolCallID + ":" + t.Name()
	}
	var out any
	var err error
	if b.Child != nil && (t.Name() != "send_message" || in.Target != "/root") {
		return operationFailure("child agents may only send messages to /root"), nil
	}
	switch t.Name() {
	case "spawn_agent":
		if t.resolve == nil {
			err = errors.New("agent resolver unavailable")
			break
		}
		var snapshot string
		snapshot, err = t.resolve(ctx, in.AgentID)
		if err != nil {
			break
		}
		var id string
		id, err = b.Runtime.Spawn(ctx, b.Scope, key, in.Name, in.AgentID, snapshot, in.Message)
		out = map[string]string{"member_id": id, "task_name": in.Name, "agent_path": "/root/" + in.Name}
	case "list_agents":
		out, err = b.List(ctx, in.Prefix)
	case "send_message", "followup_task":
		if in.Target == "/root" {
			err = b.MessageParent(ctx, key, in.Message)
			out = map[string]bool{"queued": err == nil}
			break
		}
		var id string
		id, err = b.Runtime.Send(ctx, b.Scope, key, in.Target, in.Message, in.Expected, t.Name() == "followup_task")
		out = map[string]string{"run_id": id}
	case "interrupt_agent":
		err = b.Runtime.Interrupt(ctx, b.Scope, in.Target, in.Expected)
		out = map[string]bool{"cancellation_requested": err == nil}
	case "wait_agent":
		ms := 30000
		if in.Timeout != nil {
			ms = *in.Timeout
		}
		if ms < 1000 || ms > 60000 {
			err = errors.New("timeout_ms must be between 1000 and 60000")
			break
		}
		until := time.Now().Add(time.Duration(ms) * time.Millisecond)
		if !b.WaitUntil.IsZero() {
			until = b.WaitUntil
			b.WaitUntil = time.Time{}
		}
		if exec != nil && exec.Snapshot != nil && b.ParentOwner != "" {
			snapshot, _, captureErr := exec.Snapshot()
			if captureErr != nil {
				err = captureErr
				break
			}
			// The engine validates the call name as well as this marker on resume.
			var saved map[string]json.RawMessage
			if err = json.Unmarshal(snapshot, &saved); err != nil {
				break
			}
			saved["collaboration_wait"] = json.RawMessage(`true`)
			snapshot, err = json.Marshal(saved)
			if err != nil {
				break
			}
			if err = b.SaveWait(ctx, snapshot, until); err != nil {
				break
			}
		}
		var updated bool
		updated, err = b.Wait(ctx, max(b.WaitCursor, b.Seen), max(time.Until(until), time.Duration(0)))
		interrupted := errors.Is(err, collaboration.ErrInput)
		if interrupted {
			err = nil
		}
		if err == nil {
			err = b.ClearWait(ctx)
		}
		if err == nil {
			var g *collaboration.Group
			g, err = b.Runtime.Store.Read(ctx, b.Scope)
			if err == nil {
				b.WaitCursor = g.NextSequence
			}
		}
		out = map[string]bool{"updated": updated, "timed_out": !updated && !interrupted, "interrupted": interrupted}
	}
	if err != nil {
		return operationFailure(err.Error()), nil
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &types.ToolResult{Success: true, Output: string(data)}, nil
}
