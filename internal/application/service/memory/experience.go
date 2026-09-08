package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	extractMaxObservations  = 24
	extractObservationRunes = 1200
)

// The LLM references numbered observations; persisted evidence IDs and agent
// scope are resolved by the server, never accepted from model output.
type experienceDecision struct {
	Trigger       string `json:"trigger"`
	Applicability string `json:"applicability"`
	Avoid         string `json:"avoid"`
	Outcome       string `json:"outcome"`
	Evidence      []int  `json:"evidence"`
}

type executionObservation struct {
	evidence   types.MemoryEvidence
	agentID    string
	tenantID   uint64
	content    string
	historical bool
}

func filterExperiences(ctx context.Context, items []*types.MemoryItem) []*types.MemoryItem {
	out := make([]*types.MemoryItem, 0, len(items))
	for _, item := range items {
		if types.MemoryExperienceAllowed(ctx, item) {
			out = append(out, item)
		}
	}
	return out
}

func boundedEvidence(text string, limit int) string {
	text, _ = types.RedactSensitive(text)
	runes := []rune(text)
	if len(runes) > limit {
		half := (limit - 32) / 2
		text = string(runes[:half]) + "\n[... omitted ...]\n" + string(runes[len(runes)-half:])
	}
	return text
}

// Serialize only observable actions and results, excluding thoughts, reasoning,
// binary payloads, documents and model-only tool data. An omitted result cannot
// be used by the extractor as proof that a command worked.
func messageObservations(message *types.Message) []executionObservation {
	if message == nil || (message.ExecutionContext.MemoryEnabled != nil && !*message.ExecutionContext.MemoryEnabled) ||
		message.Role != "assistant" ||
		!message.IsCompleted ||
		message.AgentID == "" {
		return nil
	}
	var observations []executionObservation
	for _, step := range message.AgentSteps {
		for _, call := range step.ToolCalls {
			if call.Result == nil || call.ID == "" || types.IsPipelineToolCallID(call.ID) {
				continue
			}
			args, _ := json.Marshal(redactArgumentValues(call.ExecutionArgs()))
			result := call.Result
			success := result.Success
			signals := map[string]interface{}{}
			for _, key := range []string{
				"exit_code", "killed", "truncated", "output_truncated", "full_output_path",
				"stdout_truncated", "stderr_truncated",
			} {
				if value, ok := result.Data[key]; ok {
					signals[key] = value
				}
			}
			if exit, ok := result.Data["exit_code"]; ok {
				switch value := exit.(type) {
				case float64:
					success = success && value == 0
				case int:
					success = success && value == 0
				case int64:
					success = success && value == 0
				case json.Number:
					success = success && value == "0"
				}
			}
			if killed, _ := result.Data["killed"].(bool); killed {
				success = false
			}

			body, _ := json.Marshal(struct {
				Tool      string                 `json:"tool"`
				Signals   map[string]interface{} `json:"signals,omitempty"`
				Arguments string                 `json:"arguments"`
				Success   bool                   `json:"success"`
				Error     string                 `json:"error,omitempty"`
				Output    string                 `json:"output"`
			}{
				call.ExecutionName(), signals, boundedEvidence(string(args), 400), success,
				boundedEvidence(result.Error, 300), boundedEvidence(result.Output, extractObservationRunes),
			})
			observations = append(observations, executionObservation{
				evidence: types.MemoryEvidence{
					SessionID:  message.SessionID,
					MessageID:  message.ID,
					ToolCallID: call.ID,
					ToolName:   call.ExecutionName(),
					Success:    success,
				},
				agentID: message.AgentID, tenantID: message.AgentTenantID, content: string(body),
			})
		}
	}
	// Keep the beginning/end and prioritize failures with their next action.
	// This avoids losing a decisive failed attempt in the middle of a long run.
	if len(observations) > extractMaxObservations {
		selected := map[int]bool{}
		for i := 0; i < 6; i++ {
			selected[i] = true
			selected[len(observations)-1-i] = true
		}
		for i, observation := range observations {
			if observation.evidence.Success {
				continue
			}
			for _, j := range []int{i, i + 1} {
				if j < len(observations) && len(selected) < extractMaxObservations {
					selected[j] = true
				}
			}
		}
		for i := 0; i < len(observations) && len(selected) < extractMaxObservations; i++ {
			selected[i] = true
		}
		kept := make([]executionObservation, 0, len(selected))
		for i, observation := range observations {
			if selected[i] {
				kept = append(kept, observation)
			}
		}
		if len(kept) > 0 {
			kept[0].content = fmt.Sprintf("[Partial task record: %d of %d calls selected; omitted calls are not "+
				"evidence.]\n", len(kept), len(observations)) + kept[0].content
		}
		observations = kept
	}

	return observations
}

func redactArgumentValues(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, value := range v {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "authorization") ||
				strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") ||
				strings.Contains(lower, "cookie") {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactArgumentValues(value)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, value := range v {
			out[i] = redactArgumentValues(value)
		}
		return out
	case string:
		return boundedEvidence(v, 500)
	default:
		return value
	}
}

func resolveExperience(segment transcriptSegment, decision *experienceDecision) (*types.MemoryExperience, error) {
	if decision == nil || len(decision.Evidence) == 0 || len(decision.Evidence) > 8 {
		return nil, errors.New("experience requires bounded tool evidence")
	}
	e := &types.MemoryExperience{
		Trigger:       decision.Trigger,
		Applicability: decision.Applicability,
		Avoid:         decision.Avoid,
		Outcome:       decision.Outcome,
	}
	seen := map[int]bool{}
	hasSuccess, hasFailure, hasNewEvidence := false, false, false
	for _, index := range decision.Evidence {
		if index < 1 || index > len(segment.observations) {
			return nil, errors.New("unknown experience evidence")
		}
		if seen[index] {
			continue
		}
		seen[index] = true
		observation := segment.observations[index-1]
		hasNewEvidence = hasNewEvidence || !observation.historical
		if len(e.Evidence) == 0 {
			e.AgentID = observation.agentID
			e.AgentTenantID = observation.tenantID
		}
		if e.AgentID != observation.agentID || e.AgentTenantID != observation.tenantID {
			return nil, errors.New("experience spans different agents")
		}
		e.Evidence = append(e.Evidence, observation.evidence)
		hasSuccess = hasSuccess || observation.evidence.Success
		hasFailure = hasFailure || !observation.evidence.Success
	}
	if !hasNewEvidence {
		return nil, errors.New("experience only references already processed evidence")
	}
	if e.Outcome == "success" && !hasSuccess {
		return nil, errors.New("successful experience lacks successful observation")
	}
	if e.Outcome == "failure" && !hasFailure {
		return nil, errors.New("failure experience lacks failed observation")
	}
	item := types.MemoryItem{Kind: types.MemoryKindExperience, Experience: e}
	if err := sanitizeExperience(&item); err != nil {
		return nil, err
	}
	return item.Experience, nil
}

func sanitizeExperience(item *types.MemoryItem) error {
	if item.Experience == nil {
		return errors.New("experience requires provenance")
	}
	cloned := *item.Experience
	e := &cloned
	e.Evidence = append([]types.MemoryEvidence(nil), e.Evidence...)
	for _, field := range []*string{&e.Trigger, &e.Applicability, &e.Avoid} {
		redacted, changed := types.RedactSensitive(*field)
		*field = redacted
		*field = types.SanitizeMemoryContent(*field)
		if changed && types.IsMostlyRedacted(*field) {
			return ErrSensitiveContent
		}
	}
	if e.Trigger == "" || e.Applicability == "" || e.AgentID == "" || len(e.Evidence) == 0 {
		return errors.New("experience requires trigger, applicability and evidence")
	}
	switch e.Outcome {
	case "success", "failure", "uncertain":
	default:
		return fmt.Errorf("invalid experience outcome %q", e.Outcome)
	}
	e.TaskSummary = boundedEvidence(e.TaskSummary, 9000)
	item.Experience = e
	return nil
}

func sameExperienceScope(a, b *types.MemoryExperience) bool {
	return a != nil && b != nil && a.AgentID == b.AgentID && a.AgentTenantID == b.AgentTenantID &&
		types.NormalizeTopicKey(a.Applicability) == types.NormalizeTopicKey(b.Applicability)
}

func extractionCandidates(segment transcriptSegment, items []*types.MemoryItem) []*types.MemoryItem {
	out := make([]*types.MemoryItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Kind != types.MemoryKindExperience {
			out = append(out, item)
			continue
		}
		for _, observation := range segment.observations {
			if item.Experience != nil && item.Experience.AgentID == observation.agentID &&
				item.Experience.AgentTenantID == observation.tenantID {
				out = append(out, item)
				break
			}
		}
	}
	return out
}
