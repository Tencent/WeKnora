package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// officialTagOpen/Close are the literals the official A2UI SDK writes into the
// prompt it generates (a2ui/constants.py A2UI_OPEN_TAG/A2UI_CLOSE_TAG) and the
// literals its parser searches for (inference_formats/direct_json/blocks.py
// OPEN_TAG_RE). They are spelled out here on purpose.
//
// Expectations in this file are literals, never the implementation constants:
// if the guidance, types.A2UITagOpen and the expectation all read the same
// symbol, changing that symbol to anything — including the markdown fence the
// official format explicitly forbids — keeps every assertion green while the
// reference renderer goes blind. So each link is pinned on its own: constants
// against literals, guidance against literals, and the taught payload against
// the parser that has to read it.
const (
	officialTagOpen  = "<a2ui-json>"
	officialTagClose = "</a2ui-json>"

	officialBasicCatalogURL = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// outputSection returns the assembled system-prompt "output" section, failing
// the test if the assembly dropped it.
func outputSection(t *testing.T) string {
	t.Helper()
	var output string
	for _, s := range BuildSystemPromptSections(nil, false, nil) {
		if s.Name == "output" {
			output = s.Content
		}
	}
	if output == "" {
		t.Fatal("output section missing")
	}
	return output
}

// TestA2UITagConstantsAreTheOfficialLiterals pins the wire literals themselves.
// Everything downstream interpolates these constants, so this is the single
// place that catches a rename drifting away from the official format.
func TestA2UITagConstantsAreTheOfficialLiterals(t *testing.T) {
	if types.A2UITagOpen != officialTagOpen {
		t.Errorf("types.A2UITagOpen = %q, official wrapper is %q", types.A2UITagOpen, officialTagOpen)
	}
	if types.A2UITagClose != officialTagClose {
		t.Errorf("types.A2UITagClose = %q, official wrapper is %q", types.A2UITagClose, officialTagClose)
	}
}

// TestAgentA2UIGuidanceInOutputSection pins that the official-A2UI teaching is
// injected into the system prompt output section and that every load-bearing
// token the backend relies on is present.
func TestAgentA2UIGuidanceInOutputSection(t *testing.T) {
	output := outputSection(t)
	for _, want := range []string{
		officialTagOpen,
		officialTagClose,
		"createSurface",
		"updateComponents",
		`"root"`,
		"Button",
		"action.event.name",
		"noop",
		"v0.9",
		officialBasicCatalogURL,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output section missing %q", want)
		}
	}
	// The fence form must be gone from what we teach: the official prompt
	// explicitly forbids it and the official parser ignores it. The positive
	// assertion above is this negative one's anchor — the same string was
	// scanned, and it demonstrably carries the wrapper that replaced the fence.
	if strings.Contains(output, "```"+types.AgentCardFenceLang) {
		t.Errorf("output still teaches the ```%s fence, which the official format forbids",
			types.AgentCardFenceLang)
	}
}

// TestAgentA2UIGuidanceStaysInSyncWithServerContract gates the taught contract:
// every action name the prompt teaches must be one the server executes
// (AgentCardActionAllowed), and the wrapper the prompt teaches must be the
// official tag pair (checked against literals, see the note at the top).
func TestAgentA2UIGuidanceStaysInSyncWithServerContract(t *testing.T) {
	guidance := agentA2UIGuidance()
	m := regexp.MustCompile(`action\.event\.name must be one of: ([a-z_,\s]+)\.`).FindStringSubmatch(guidance)
	if m == nil {
		t.Fatal("action-name enumeration missing from guidance")
	}
	taught := map[string]bool{}
	for _, tok := range strings.Split(m[1], ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		taught[tok] = true
		if !types.AgentCardActionAllowed(tok) {
			t.Errorf("guidance teaches action %q that the server refuses", tok)
		}
	}
	if len(taught) == 0 {
		t.Fatal("no action names extracted from guidance")
	}
	tm := regexp.MustCompile(`wrap the card in the literal (<[a-z0-9-]+>) and (</[a-z0-9-]+>) tags`).
		FindStringSubmatch(guidance)
	if tm == nil {
		t.Fatal("wrapper tags not extractable from guidance — the taught block form changed shape")
	}
	if tm[1] != officialTagOpen || tm[2] != officialTagClose {
		t.Errorf("guidance teaches wrapper %s…%s, official format is %s…%s",
			tm[1], tm[2], officialTagOpen, officialTagClose)
	}
}

// TestAgentA2UIGuidanceRoundTripsThroughOurParser closes the loop the schema
// test starts: the schema test proves the taught frames are officially valid,
// this one proves our own reader accepts the taught block in the taught wrapper
// and can re-derive the taught button from it.
func TestAgentA2UIGuidanceRoundTripsThroughOurParser(t *testing.T) {
	guidance := agentA2UIGuidance()
	arrayJSON := extractFirstJSONArray(guidance)
	if arrayJSON == "" {
		t.Fatal("no JSON array found in A2UI guidance — producer contract is empty")
	}
	content := officialTagOpen + "\n" + arrayJSON + "\n" + officialTagClose

	frames := types.ExtractA2UIMessages(content)
	if len(frames) < 2 {
		t.Fatalf("taught block yielded %d frames, want >=2 (createSurface + updateComponents)", len(frames))
	}
	btn := types.FindA2UIButton(content, "s1", "b1")
	if btn == nil {
		t.Fatalf("taught button s1/b1 not re-derivable from the taught block; frames: %s", arrayJSON)
	}
	if btn.ActionName != "noop" {
		t.Errorf("taught button action = %q, want %q", btn.ActionName, "noop")
	}

	// Anchor for the assertions above: the very same payload with the wrapper
	// removed must yield nothing. Without this, "the parser reads the taught
	// block" could be satisfied by any text at all, and the wrapper — the one
	// thing the official format is about — would be untested.
	if got := types.ExtractA2UIMessages(arrayJSON); len(got) != 0 {
		t.Errorf("bare array with no wrapper yielded %d frames, want 0: the wrapper is not load-bearing", len(got))
	}
}

// TestAgentA2UIGuidanceExampleSatisfiesTaughtOrdering keeps the example and the
// rule it teaches in step, because the example is what the model copies.
//
// The rule comes from the official generated prompt's rules section
// (conformance/test_data/prompts/direct_json/allowed_messages.txt: "The 'root'
// component MUST be the FIRST element. Parent components MUST appear before
// their child components."). The Button-label exception is not my reading: the
// official protocol example (specification/v0_9/docs/a2ui_protocol.md, the contact
// form) defines submit_button_label at list index 23 and submit_button
// at 24, and form_container.children lists submit_button only. Following that
// example keeps the teaching a strict instance of the official shape instead of
// a third, self-invented one.
func TestAgentA2UIGuidanceExampleSatisfiesTaughtOrdering(t *testing.T) {
	if !strings.Contains(agentA2UIGuidance(), "MUST be FIRST") {
		t.Fatal("guidance no longer states the top-down ordering rule it is checked against")
	}
	arrayJSON := extractFirstJSONArray(agentA2UIGuidance())
	if arrayJSON == "" {
		t.Fatal("no JSON array found in A2UI guidance")
	}
	var frames []map[string]any
	if err := json.Unmarshal([]byte(arrayJSON), &frames); err != nil {
		t.Fatalf("taught array is not valid JSON: %v", err)
	}
	var components []any
	for _, f := range frames {
		if uc, ok := f["updateComponents"].(map[string]any); ok {
			components, _ = uc["components"].([]any)
		}
	}
	if len(components) == 0 {
		t.Fatal("taught updateComponents carries no components list")
	}
	pos := map[string]int{}
	for i, c := range components {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok {
			if _, dup := pos[id]; dup {
				t.Fatalf("component id %q appears twice in the taught example", id)
			}
			pos[id] = i
		}
	}
	if pos["root"] != 0 {
		t.Fatalf(`taught example has root at index %d, the rule teaches it MUST be FIRST`, pos["root"])
	}
	for i, c := range components {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		// Container children: forward references only (official rule).
		if list, ok := m["children"].([]any); ok {
			for _, k := range list {
				kid, ok := k.(string)
				if !ok {
					continue
				}
				kidPos, ok := pos[kid]
				if !ok {
					t.Errorf("component %q lists child %q in children, which the example never defines", id, kid)
					continue
				}
				if kidPos < i {
					t.Errorf("container %q (index %d) is listed AFTER its child %q (index %d)",
						id, i, kid, kidPos)
				}
			}
		}
		if child, ok := m["child"].(string); ok {
			if _, ok := pos[child]; !ok {
				t.Errorf("component %q points child at %q, which the example never defines", id, child)
			}
		}
		// A Button's label is the one reference the official example points
		// BACKWARD: the label Text is defined immediately before the Button, so
		// a streaming renderer already has the label when the Button arrives.
		if m["component"] == "Button" {
			label, ok := m["child"].(string)
			if !ok {
				t.Errorf("Button %q carries no child label reference", id)
				continue
			}
			if labelPos, ok := pos[label]; !ok || labelPos > i {
				t.Errorf("Button %q (index %d) labels %q, which is not defined before it", id, i, label)
			}
			for _, c2 := range components {
				m2, ok := c2.(map[string]any)
				if !ok {
					continue
				}
				list, ok := m2["children"].([]any)
				if !ok {
					continue
				}
				for _, k := range list {
					if k == label {
						t.Errorf("label %q of Button %q is also listed in container %q's children; "+
							"the official example leaves it out — the Button renders its own label",
							label, id, m2["id"])
					}
				}
			}
		}
	}
}

// TestAgentA2UIGuidanceTeachesConfirm pins the confirm-action teaching with
// literals: the executor (handler/session/agent_card.go) reads
// action.event.context.message verbatim from the persisted card, so a prompt
// rewrite that drops or rewords the contract must turn red here, not on a
// live model round.
func TestAgentA2UIGuidanceTeachesConfirm(t *testing.T) {
	output := outputSection(t)
	for _, want := range []string{
		"must be one of: noop, confirm.",
		`Use "confirm" only when the user must approve a plan and the agent`,
		"action.event.context.message",
		"a non-empty string",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("A2UI output section is missing %q", want)
		}
	}
}

// TestAgentA2UIGuidanceTeachesForm pins the form-card teaching: the five input
// controls, the binding shape, the replay/reject contract, the composition
// header, and the hard caps — all as literals, because this file is the
// producer-side contract and a drift in any of these lines silently breaks
// whatever the model was taught to emit.
func TestAgentA2UIGuidanceTeachesForm(t *testing.T) {
	output := outputSection(t)
	for _, want := range []string{
		"TextField",
		"CheckBox",
		"ChoicePicker",
		"Slider",
		"DateTimeInput",
		`"value":{"path":"/name"}`,
		`"name":{"path":"/name"}`,
		"At most 8 input fields per card.",
		"Never use the obscured (password) variant",
		"Never attach inputs to a noop button",
		"the literal 未填写",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("A2UI output section is missing %q", want)
		}
	}
	// The taught form card must exist as a JSON array in the output and must
	// be parseable into the same button spec the executor re-derives from
	// persisted content. The output section also contains bracket pairs from
	// other guidance (e.g. ![alt](url)), so identify the card by its button
	// rather than by array index; the parser needs the <a2ui-json> wrapper.
	arrays := extractJSONArrays(output)
	var btn *types.A2uiResolvedButton
	for _, arr := range arrays {
		candidate := types.FindA2UIButton(types.A2UITagOpen+"\n"+arr+"\n"+types.A2UITagClose, "s1", "b1")
		if candidate != nil && candidate.ActionName == "confirm" {
			btn = candidate
			break
		}
	}
	if btn == nil {
		t.Fatalf("taught form button s1/b1 not re-derivable from the taught form card; arrays: %d", len(arrays))
	}
	if btn.ActionName != "confirm" {
		t.Errorf("taught form button action = %q, want %q", btn.ActionName, "confirm")
	}
	if got := fmt.Sprint(btn.BoundInputPaths); got != "[/name /room]" {
		t.Errorf("taught form button BoundInputPaths = %s, want %s", got, "[/name /room]")
	}
	if len(btn.FieldLabels) != 2 {
		t.Errorf("taught form button FieldLabels has %d entries, want 2", len(btn.FieldLabels))
	}
}
