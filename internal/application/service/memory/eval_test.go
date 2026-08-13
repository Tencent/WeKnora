package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The distillation prompt is the part of this feature nobody can review by
// reading it: whether a rule helps is an empirical question. This file is the
// harness for answering it.
//
// It has two modes. By default it runs offline and only checks that the golden
// set is well-formed and that the prompt actually carries what each case needs,
// which is cheap and keeps the file honest. Given a real model it scores the
// prompt against the set:
//
//	WEKNORA_MEMORY_EVAL_MODEL=<model id> \
//	WEKNORA_MEMORY_EVAL_BASE_URL=... WEKNORA_MEMORY_EVAL_API_KEY=... \
//	go test ./internal/application/service/memory/ -run TestPromptEval -v
//
// Scores are printed per case and in total. There is no pass threshold on
// purpose: the number is only meaningful compared against the previous run of
// the same set, and baking in a bar would just make people lower it.

type evalExistingNote struct {
	Kind    string `json:"kind"`
	Topic   string `json:"topic"`
	Content string `json:"content"`
}

type evalCase struct {
	Name             string             `json:"name"`
	Context          []string           `json:"context"`
	Lines            []string           `json:"lines"`
	Existing         []evalExistingNote `json:"existing"`
	ExpectCountMin   *int               `json:"expect_count_min"`
	ExpectCountMax   *int               `json:"expect_count_max"`
	ExpectKinds      []string           `json:"expect_kinds"`
	ExpectActions    []string           `json:"expect_actions"`
	ExpectTargets    []int              `json:"expect_targets"`
	ExpectSubstrings []string           `json:"expect_substrings"`
	RejectSubstrings []string           `json:"reject_substrings"`
	ExpectExpiry     bool               `json:"expect_expiry"`
}

type evalSet struct {
	Cases []evalCase `json:"cases"`
}

func loadEvalSet(t *testing.T) evalSet {
	t.Helper()
	raw, err := os.ReadFile("evalset.json")
	require.NoError(t, err)
	var set evalSet
	require.NoError(t, json.Unmarshal(raw, &set))
	require.NotEmpty(t, set.Cases)
	return set
}

// segmentForCase renders a case the same way a real run would, so the harness
// grades the prompt the product actually sends.
func segmentForCase(c evalCase) transcriptSegment {
	base := time.Now().Add(-time.Hour)
	segment := transcriptSegment{sessionID: "eval", context: c.Context}
	for i, line := range c.Lines {
		segment.lines = append(segment.lines, transcriptLine{
			sessionID: "eval",
			messageID: fmt.Sprintf("eval-%d", i+1),
			at:        base.Add(time.Duration(i) * time.Minute),
			content:   line,
		})
	}
	return segment
}

func existingForCase(c evalCase) []*types.MemoryItem {
	items := make([]*types.MemoryItem, 0, len(c.Existing))
	for _, note := range c.Existing {
		items = append(items, &types.MemoryItem{
			Kind: note.Kind, Topic: note.Topic, Content: note.Content,
		})
	}
	return items
}

// TestEvalSetIsWellFormed runs everywhere and keeps the golden set from rotting:
// a case whose text never reaches the prompt grades nothing.
func TestEvalSetIsWellFormed(t *testing.T) {
	set := loadEvalSet(t)
	seen := make(map[string]struct{}, len(set.Cases))
	for _, c := range set.Cases {
		require.NotEmpty(t, c.Name)
		_, duplicate := seen[c.Name]
		require.False(t, duplicate, "duplicate case name %q", c.Name)
		seen[c.Name] = struct{}{}
		require.NotEmpty(t, c.Lines, "case %q has nothing to extract from", c.Name)

		prompt := buildExtractionPrompt(segmentForCase(c), existingForCase(c), nil, nil, "")
		for _, line := range c.Lines {
			require.Contains(t, prompt, line, "case %q: line missing from the prompt", c.Name)
		}
		for _, line := range c.Context {
			require.Contains(t, prompt, line, "case %q: context missing from the prompt", c.Name)
		}
		for _, note := range c.Existing {
			require.Contains(t, prompt, note.Content,
				"case %q: existing note missing from the prompt", c.Name)
		}
		for i := range c.ExpectTargets {
			require.Less(t, c.ExpectTargets[i], len(c.Existing),
				"case %q expects target %d but supplies fewer notes", c.Name, c.ExpectTargets[i])
		}
	}
}

// evalResult is one graded case.
type evalResult struct {
	name     string
	passed   bool
	failures []string
}

func gradeCase(c evalCase, decisions []extractionDecision) evalResult {
	result := evalResult{name: c.name(), passed: true}

	kept := make([]extractionDecision, 0, len(decisions))
	for _, decision := range decisions {
		action := strings.ToLower(strings.TrimSpace(decision.Action))
		if action == "" || action == "none" || action == "noop" {
			continue
		}
		kept = append(kept, decision)
	}

	fail := func(format string, args ...any) {
		result.passed = false
		result.failures = append(result.failures, fmt.Sprintf(format, args...))
	}

	if c.ExpectCountMin != nil && len(kept) < *c.ExpectCountMin {
		fail("expected at least %d memories, got %d", *c.ExpectCountMin, len(kept))
	}
	if c.ExpectCountMax != nil && len(kept) > *c.ExpectCountMax {
		fail("expected at most %d memories, got %d", *c.ExpectCountMax, len(kept))
	}

	blob := strings.ToLower(decisionsBlob(kept))
	for _, want := range c.ExpectSubstrings {
		if !strings.Contains(blob, strings.ToLower(want)) {
			fail("missing %q", want)
		}
	}
	for _, reject := range c.RejectSubstrings {
		if strings.Contains(blob, strings.ToLower(reject)) {
			fail("should not have recorded %q", reject)
		}
	}
	for _, kind := range c.ExpectKinds {
		if !hasField(kept, func(d extractionDecision) bool {
			return strings.EqualFold(d.Kind, kind)
		}) {
			fail("no memory of kind %q", kind)
		}
	}
	for _, action := range c.ExpectActions {
		if !hasField(kept, func(d extractionDecision) bool {
			return strings.EqualFold(d.Action, action)
		}) {
			fail("no %q action", action)
		}
	}
	for _, target := range c.ExpectTargets {
		if !hasField(kept, func(d extractionDecision) bool {
			return d.Target != nil && *d.Target == target
		}) {
			fail("no decision targeting note %d", target)
		}
	}
	if c.ExpectExpiry && !hasField(kept, func(d extractionDecision) bool {
		return parseExpiry(d.ExpiresAt) != nil
	}) {
		fail("no usable expires_at")
	}
	return result
}

func (c evalCase) name() string { return c.Name }

func decisionsBlob(decisions []extractionDecision) string {
	var builder strings.Builder
	for _, decision := range decisions {
		fmt.Fprintf(&builder, "%s %s %s %s %s\n",
			decision.Action, decision.Kind, decision.Topic, decision.Content, decision.ExpiresAt)
	}
	return builder.String()
}

func hasField(decisions []extractionDecision, match func(extractionDecision) bool) bool {
	for _, decision := range decisions {
		if match(decision) {
			return true
		}
	}
	return false
}

// TestPromptEval scores the prompt against the golden set using a real model.
// Skipped unless WEKNORA_MEMORY_EVAL_MODEL is set.
func TestPromptEval(t *testing.T) {
	modelID := strings.TrimSpace(os.Getenv("WEKNORA_MEMORY_EVAL_MODEL"))
	if modelID == "" {
		t.Skip("set WEKNORA_MEMORY_EVAL_MODEL (plus base URL / API key) to score the prompt")
	}
	chatModel, err := newEvalChatModel(modelID)
	require.NoError(t, err)

	set := loadEvalSet(t)
	passed := 0
	for _, c := range set.Cases {
		prompt := buildExtractionPrompt(segmentForCase(c), existingForCase(c), nil, nil, "")
		decisions, err := runEvalExtraction(context.Background(), chatModel, prompt)
		if err != nil {
			t.Errorf("case %q: %v", c.Name, err)
			continue
		}
		result := gradeCase(c, decisions)
		if result.passed {
			passed++
			t.Logf("PASS  %s", result.name)
			continue
		}
		t.Errorf("FAIL  %s\n      %s", result.name, strings.Join(result.failures, "\n      "))
	}
	t.Logf("prompt eval: %d/%d cases passed with model %s", passed, len(set.Cases), modelID)
}
