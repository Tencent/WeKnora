package types

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Interactive agent cards in the official A2UI v0.9 wire format (upstream
// issue #3017 — this branch makes WeKnora a standard A2UI
// producer; our earlier fence-based approach stays the fallback).
//
// The contract: the model emits the card inside the official direct_json
// wrapper tags — the literals the official SDK prompt generator and its parser
// share (a2ui/constants.py A2UI_OPEN_TAG/A2UI_CLOSE_TAG feed both the generated
// prompt and inference_formats/direct_json/blocks.py OPEN_TAG_RE), so the
// teaching and the reference consumer cannot drift apart:
//
//	<a2ui-json>
//	[
//	  {"version":"v0.9","createSurface":{"surfaceId":"s1",
//	    "catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},
//	  {"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[
//	    {"id":"root","component":"Card","child":"col1"},
//	    {"id":"col1","component":"Column","children":["t1","t2","btn1"]},
//	    {"id":"t1","component":"Text","text":"...","variant":"h2"},
//	    {"id":"t2","component":"Text","text":"..."},
//	    {"id":"btn1_label","component":"Text","text":"Confirm"},
//	    {"id":"btn1","component":"Button","child":"btn1_label","variant":"primary",
//	     "action":{"event":{"name":"noop"}}}
//	  ]}}
//	]
//	</a2ui-json>
//
// Component order inside "components" is part of the official contract, not
// cosmetics: root FIRST and containers before the ids in their children is what
// lets a streaming renderer paint the card incrementally
// (a2ui/constants.py DEFAULT_WORKFLOW_RULES). The example above follows the
// official protocol example in one more respect: a Button's label Text is
// defined immediately BEFORE the Button and is not listed in the enclosing
// container's children — the Button renders it. Our own parser merges the list
// into a map, so a model that flips the order costs us nothing locally and costs
// a streaming consumer the increment — the teaching states the rule and the
// guidance test asserts the example obeys it.
//
// The older ```a2ui fenced form (our own invention — the official format
// never had it) is still extracted, so messages already stored keep replaying
// and the shipped UI keeps working; the teaching emits the tag form only. A
// fence found INSIDE the tags is un-fenced here, exactly like the official
// parser, which strips fences inside/around a tagged payload
// (blocks.py _LEADING_FENCE_RE/_TRAILING_FENCE_RE): in the official format a
// fence is noise, never the delimiter, and a fence-only message is precisely
// what the official parser yields zero parts for.
//
// Card takes a SINGLE "child" id — the official basic catalog requires
// ["component","child"] and forbids extra keys, so "children":[...] on the
// Card is schema-invalid and a conforming external renderer rejects it. Our
// own parser does not validate the shape at runtime (it checks the frame
// version and re-derives buttons by component type), so such a card can look
// fine here and still fail to render elsewhere: the shape rule is enforced
// offline by a2ui_producer_schema_test.go, and the teaching in
// internal/agent/prompts.go states the same rule — keep the two in step.
//
// The block payload is a JSON array of official A2UI server→client frames; the
// block is persisted verbatim inside Message.Content, so external A2UI clients
// (the real consumers) and history replay see exactly what the model emitted
// (fallback: the source stays visible — a code block for the fence form, plain
// text for the tag form). ExecuteAgentCardAction reloads
// the content and re-derives the clicked button from the persisted
// component tree, so a tampered client can never assert more than what the
// model actually offered — the client's claimed action name is cross-checked
// against the persisted one, and only the persisted value decides.

const (
	// AgentCardFenceLang is the fenced-code language tag of the LEGACY block
	// form: our own invention, kept accepted so stored messages keep
	// replaying. The teaching no longer emits it. Keep the literal in sync with
	// agentA2UIMessageFenceRE.
	AgentCardFenceLang = "a2ui"
	// A2UITagOpen / A2UITagClose are the official direct_json wrapper tags —
	// the format the official SDK generates prompts for and parses with, so
	// emitting these literals is what makes an external A2UI client able to find
	// our frames at all. The teaching in internal/agent/prompts.go interpolates
	// them; the prompt/guidance test asserts the emitted text carries them.
	A2UITagOpen  = "<a2ui-json>"
	A2UITagClose = "</a2ui-json>"
	// A2UIMessageVersion is the protocol version every frame carries; frames
	// with any other version are dropped (a wrong-version frame has no defined
	// semantics and is never executed).
	A2UIMessageVersion = "v0.9"
)

// A2uiMessage is one official A2UI server→client frame: `oneOf` exactly one
// of the four payloads, plus the mandatory version stamp.
type A2uiMessage struct {
	Version          string                `json:"version"`
	CreateSurface    *A2uiCreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *A2uiUpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *A2uiUpdateDataModel  `json:"updateDataModel,omitempty"`
	DeleteSurface    *A2uiDeleteSurface    `json:"deleteSurface,omitempty"`
}

// A2uiCreateSurface opens a UI canvas.
type A2uiCreateSurface struct {
	SurfaceID string          `json:"surfaceId"`
	CatalogID string          `json:"catalogId"`
	Theme     json.RawMessage `json:"theme,omitempty"`
}

// A2uiUpdateComponents delivers (part of) the flat component list for a
// surface. The protocol requires an `id:"root"` component; the backend does
// not enforce that here — it only locates buttons. Root/catalog compliance
// lives in the prompt contract and the schema test.
type A2uiUpdateComponents struct {
	SurfaceID  string          `json:"surfaceId"`
	Components []A2uiComponent `json:"components"`
}

// A2uiUpdateDataModel applies a data-model change (JSON pointer `path`,
// `value` is the raw payload). Kept as raw bytes: the executor does not
// interpret data model content.
type A2uiUpdateDataModel struct {
	SurfaceID string          `json:"surfaceId"`
	Path      string          `json:"path,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
}

// A2uiDeleteSurface closes a canvas.
type A2uiDeleteSurface struct {
	SurfaceID string `json:"surfaceId"`
}

// A2uiComponent is one node of the flat component list. Only the fields the
// executor cares about are decoded; every other field (children, variant, …)
// is dropped by the decoder, which is what we want — re-derivation must
// depend on id, component, child and action.event.name, never on decorative
// content. Text is the one display field we keep: a Button's label is
// resolved from its child Text component's text, for audit/response text
// only.
type A2uiComponent struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Text      string `json:"text,omitempty"`
	// Button only: id of the Text component that renders its label.
	Child  string      `json:"child,omitempty"`
	Action *A2uiAction `json:"action,omitempty"`
}

// A2uiAction is the official `oneOf [event, functionCall]`. Only `event` is
// executable server-side; `functionCall` is a client-side local function and
// a Button carrying one has no server action to execute.
type A2uiAction struct {
	Event        *A2uiEvent      `json:"event,omitempty"`
	FunctionCall json.RawMessage `json:"functionCall,omitempty"`
}

// A2uiEvent is a server-side action: `name` is the action identifier the
// client reports back and the server looks up in its policy table.
// `Context` carries resolved data-binding values; the confirm executor
// reads its follow-up instruction from `Context["message"]`.
type A2uiEvent struct {
	Name    string                     `json:"name"`
	Context map[string]json.RawMessage `json:"context,omitempty"`
}

// agentCardActionPolicy is the server-side execution policy: action name →
// the minimum tenant role allowed to trigger it. This is the server-side
// replacement for the earlier model-declared permission_level — the protocol
// has no permission field, so the policy is entirely server-owned and a
// hallucinated (or tampered) generator can never raise or lower it. Adding
// an executor means adding a policy entry here AND a case in the handler
// switch; v0 ships "noop" (no side effects) and "confirm" (server-side
// follow-up run), both at the route's own floor.
var agentCardActionPolicy = map[string]TenantRole{
	"noop":    TenantRoleViewer,
	"confirm": TenantRoleViewer,
}

// AgentCardActionAllowed reports whether actionName may be executed
// server-side (see agentCardActionPolicy). The map is unexported on purpose:
// callers go through this predicate so no other package can mutate the
// policy at runtime.
func AgentCardActionAllowed(actionName string) bool {
	_, ok := agentCardActionPolicy[actionName]
	return ok
}

// AgentCardRequiredRole maps an action name onto the minimum tenant role via
// the policy table. Unknown names require Admin: an intentional policy
// floor, not a fail-closed accident. (The whitelist refuses unknown names
// before the role check runs, so this floor only matters for future policy
// entries that omit a role.)
func AgentCardRequiredRole(actionName string) TenantRole {
	if r, ok := agentCardActionPolicy[actionName]; ok {
		return r
	}
	return TenantRoleAdmin
}

// SetAgentCardActionPolicyForTest temporarily swaps the execution policy map
// and returns a restore function. Tests in other packages (the handler tests)
// use it to exercise the role-floor gate with an action mapped above the
// caller's role, since v0's shipped "noop" sits at the route's own viewer
// floor and can never trigger an RBAC denial on its own. Production code must
// never call this; the returned restore MUST be invoked (defer it).
func SetAgentCardActionPolicyForTest(m map[string]TenantRole) (restore func()) {
	prev := agentCardActionPolicy
	agentCardActionPolicy = m
	return func() { agentCardActionPolicy = prev }
}

// agentA2UIMessageFenceRE captures each top-level ```a2ui fence body. The
// opener must sit at the start of a line with at most 3 spaces of
// indentation: the frontend's markdown renderer follows CommonMark rules,
// and an opener indented 4+ spaces or sharing a line with prose becomes a
// code token with an empty lang — not an A2UI block — so the backend must
// never extract those either. The closer must stand alone on its line, so a
// nested ```json fence inside a component's text does not end the block
// early; an unclosed fence instead swallows content up to the next real
// closer, making the whole block unparseable — the block is then skipped
// (fail closed) rather than silently stealing a later block's identity.
var agentA2UIMessageFenceRE = regexp.MustCompile(
	"(?sm)^ {0,3}\x60\x60\x60" + AgentCardFenceLang +
		"[ \t]*\r?\n(.*?)\r?\n {0,3}\x60\x60\x60[ \t]*(?:\r?\n|$)")

// The tag regexes below are the official ones, copied: blocks.py:26 OPEN_TAG_RE
// and :27 CLOSE_TAG_RE. The tolerance is theirs, not ours — case-insensitive,
// attributes allowed on the open tag, whitespace allowed before the close tag.
// Nothing here interprets the payload as HTML, so a card whose text contains
// "<script>" stays data.
var (
	agentA2UIOpenTagRE  = regexp.MustCompile(`(?i)<a2ui-json(?:\s[^>]*)?>`)
	agentA2UICloseTagRE = regexp.MustCompile(`(?i)</a2ui-json\s*>`)
)

// agentA2UIInner{Open,Close}FenceRE strip a markdown fence that wraps the
// payload INSIDE the tags — blocks.py:31-32 _LEADING_FENCE_RE/_TRAILING_FENCE_RE
// copied. Models do this even when told not to, and the official parser absorbs
// it, so we must too: a card we reject here is a card the official renderer
// would have shown. Anchored, so a fence inside a component's text is untouched.
var (
	agentA2UIInnerOpenFenceRE  = regexp.MustCompile("^\x60{3}[a-zA-Z-]*\\s*")
	agentA2UIInnerCloseFenceRE = regexp.MustCompile("\\s*\x60{3}[a-zA-Z-]*$")
)

// scanA2UITagBlocks locates every official <a2ui-json> block, in document order,
// the way the official parser does it — an open tag found by regex, then the
// close tag searched for by a scanner that knows JSON strings (blocks.py
// BlockScanner, whose docstring states the rule: "A tag written inside a JSON
// string does not count").
//
// The string awareness is load-bearing, not paranoia: a card that quotes the
// format (answering "how do I emit an A2UI card?" — WeKnora's own docs question)
// carries a full tag pair inside a JSON string value. A plain regex ends the
// block at that quoted close tag, which truncates the payload, which fails to
// parse, which loses a card the official renderer shows.
//
// An open tag with no close tag yields nothing: the block is still arriving in
// the streaming case, and in the stored case it is a hallucination — fail closed.
func scanA2UITagBlocks(content string) []a2uiBlock {
	var out []a2uiBlock
	for i := 0; i < len(content); {
		open := agentA2UIOpenTagRE.FindStringIndex(content[i:])
		if open == nil {
			break
		}
		start, payload := i+open[0], i+open[1]
		closeIdx, closeLen := findA2UICloseTag(content, payload)
		if closeIdx < 0 {
			break // unclosed block: fail closed
		}
		out = append(out, a2uiBlock{start, closeIdx + closeLen, cleanA2UIPayload(content[payload:closeIdx])})
		i = closeIdx + closeLen
	}
	return out
}

// findA2UICloseTag returns the offset and length of the first </a2ui-json> at or
// after start that is not inside a JSON string. The quote/escape walk mirrors
// blocks.py BlockScanner.scan: inside a string, a backslash skips the escaped
// character, and only an unescaped quote ends the string.
func findA2UICloseTag(s string, start int) (int, int) {
	for i, inString := start, false; i < len(s); {
		if inString {
			quote := strings.IndexByte(s[i:], '"')
			q := i + quote
			if quote < 0 {
				q = len(s)
			}
			if esc := strings.IndexByte(s[i:q], '\\'); esc >= 0 {
				i += esc + 2 // escaped character: skip both bytes
				continue
			}
			if quote < 0 {
				break // string never closes: no reliable close tag
			}
			inString = false
			i = q + 1
			continue
		}
		switch s[i] {
		case '"':
			inString = true
			i++
		case '<':
			if m := agentA2UICloseTagRE.FindStringIndex(s[i:]); m != nil && m[0] == 0 {
				return i, m[1]
			}
			i++
		default:
			i++
		}
	}
	return -1, 0
}

// cleanA2UIPayload is blocks.py clean(): trim whitespace, strip one leading and
// one trailing markdown fence, trim again; a payload that is nothing but fence
// backticks is empty.
func cleanA2UIPayload(body string) string {
	body = strings.TrimSpace(body)
	body = agentA2UIInnerOpenFenceRE.ReplaceAllString(body, "")
	body = agentA2UIInnerCloseFenceRE.ReplaceAllString(body, "")
	body = strings.TrimSpace(body)
	if strings.Trim(body, "`") == "" {
		return ""
	}
	return body
}

// a2uiBlock is one located block: its span in the source (used to resolve
// overlapping blocks) and its payload text.
type a2uiBlock struct {
	start, end int
	body       string
}

// extractA2UIBlocks locates every A2UI block in an assistant message, in both
// accepted forms, and returns one block per overlap group, in document order.
//
// Document order is load-bearing: FindA2UIButton merges components per surface
// last-write-wins, so a message that redefines a button in a later block must
// keep that meaning here.
//
// Inside an overlap group (two accepted forms whose spans touch — a tag block
// wrapped in a fence, or a fence whose JSON quotes the tag literals as text)
// exactly one block is emitted, because emitting both would deliver the same
// frames twice and double the audit surface. The winner is the OUTERMOST block
// that actually carries frames, else the outermost block:
//
//   - a fence whose body is a <a2ui-json> block (the model fenced the tags
//     despite being told not to) → the fence body is not JSON, the tag payload
//     is → the tag wins. This is the official reading: for the official parser
//     the tag is the delimiter and a fence is noise, so fence-only content
//     yields zero parts and fenced tags yield their tags.
//   - a fence whose JSON carries the tag literals inside a string value (a card
//     that documents the format, like our own teaching text) → the fence parses,
//     so the fence wins and the tag literals stay inert string content.
//
// Preferring the block that parses is also the fail-closed reading: an
// unparseable outer block contributes nothing, it never steals the inner block's
// identity.
func extractA2UIBlocks(content string) []a2uiBlock {
	var blocks []a2uiBlock
	for _, m := range agentA2UIMessageFenceRE.FindAllStringSubmatchIndex(content, -1) {
		blocks = append(blocks, a2uiBlock{m[0], m[1], strings.TrimSpace(content[m[2]:m[3]])})
	}
	blocks = append(blocks, scanA2UITagBlocks(content)...)
	// Outermost first within a group: sorting by start asc, then end desc, puts
	// the enclosing block at the head of its group.
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].start != blocks[j].start {
			return blocks[i].start < blocks[j].start
		}
		return blocks[i].end > blocks[j].end
	})
	var out []a2uiBlock
	for i := 0; i < len(blocks); {
		group, groupEnd := []a2uiBlock{blocks[i]}, blocks[i].end
		j := i + 1
		for j < len(blocks) && blocks[j].start < groupEnd {
			group = append(group, blocks[j])
			if blocks[j].end > groupEnd {
				groupEnd = blocks[j].end
			}
			j++
		}
		chosen := group[0]
		for _, b := range group {
			if len(parseA2UIMessages(b.body)) > 0 {
				chosen = b
				break
			}
		}
		out = append(out, chosen)
		i = j
	}
	return out
}

// A2uiResolvedButton is one Button located in a persisted component tree.
type A2uiResolvedButton struct {
	SurfaceID   string
	ComponentID string
	ActionName  string // re-derived from the persisted action.event.name
	Label       string // resolved from the child Text component, "" if absent
	// Context is the persisted action.event.context, verbatim and nil when the
	// button carries none. The confirm executor is its only v0 reader; the
	// client-request copy of this field is never consulted.
	Context map[string]json.RawMessage
}

// ExtractA2UIMessages parses every A2UI block from assistant markdown — in
// either accepted form, the official <a2ui-json> tags or the legacy ```a2ui
// fence — into the official message frames it contains. The payload is a JSON
// array of A2uiMessage objects; a single object is also accepted (tolerance,
// not a contract the prompt teaches). Frames that fail JSON decode, or whose
// version is not A2UIMessageVersion, are dropped silently: the block still
// renders as plain text client-side, which is the safe fallback, and a
// hallucinated block must never break history rendering or execution.
func ExtractA2UIMessages(content string) []A2uiMessage {
	// Cheap pre-filter for the (overwhelmingly common) case of an answer with no
	// card at all. Deliberately looser than the block regexes — it only has to
	// not miss a real block. Two loosenings matter: it is case-folded, because
	// the tag is matched case-insensitively; and it looks for the tag WITHOUT
	// its closing ">", because the official open tag may carry attributes
	// (`<a2ui-json data-source="...">`), so requiring ">" would drop a card the
	// official parser accepts.
	lower := strings.ToLower(content)
	if !strings.Contains(content, "```"+AgentCardFenceLang) &&
		!strings.Contains(lower, strings.ToLower(strings.TrimSuffix(A2UITagOpen, ">"))) {
		return nil
	}
	var out []A2uiMessage
	for _, b := range extractA2UIBlocks(content) {
		out = append(out, parseA2UIMessages(b.body)...)
	}
	return out
}

// parseA2UIMessages decodes one block payload into frames. Contract shape is a
// JSON array; a single frame object is also accepted (tolerance, not a contract
// the prompt teaches). Anything that fails both decodes yields nil — the block
// then renders as plain text client-side, which is the safe fallback, and a
// hallucinated block must never break history rendering or execution. Frames
// whose version is not A2UIMessageVersion are dropped: a wrong-version frame has
// no defined semantics and is never executed.
func parseA2UIMessages(body string) []A2uiMessage {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var frames []A2uiMessage
	if err := json.Unmarshal([]byte(body), &frames); err != nil {
		var single A2uiMessage
		if err2 := json.Unmarshal([]byte(body), &single); err2 != nil {
			return nil
		}
		frames = []A2uiMessage{single}
	}
	var out []A2uiMessage
	for _, f := range frames {
		if f.Version != A2UIMessageVersion {
			continue
		}
		out = append(out, f)
	}
	return out
}

// FindA2UIButton re-derives a clicked button from the persisted component
// tree. Components from every updateComponents frame of the surface are
// merged with the official semantics — last write wins per component id, so
// a later frame can replace an earlier button definition. It then requires
// the located component to be a Button carrying an event action. Returns
// nil when the surface or component is absent, the component is not a
// Button, or the button has no event action — the caller treats nil as
// "unresolved" (404 + audit), as before.
func FindA2UIButton(content, surfaceID, componentID string) *A2uiResolvedButton {
	merged := map[string]*A2uiComponent{}
	for _, frame := range ExtractA2UIMessages(content) {
		uc := frame.UpdateComponents
		if uc == nil || uc.SurfaceID != surfaceID {
			continue
		}
		for i := range uc.Components {
			merged[uc.Components[i].ID] = &uc.Components[i] // last write wins
		}
	}
	c, ok := merged[componentID]
	if !ok || c.Component != "Button" {
		return nil
	}
	if c.Action == nil || c.Action.Event == nil || strings.TrimSpace(c.Action.Event.Name) == "" {
		return nil // a Button without an event action has nothing to execute
	}
	btn := &A2uiResolvedButton{
		SurfaceID:   surfaceID,
		ComponentID: componentID,
		ActionName:  strings.TrimSpace(c.Action.Event.Name),
		Context:     c.Action.Event.Context,
	}
	if c.Child != "" {
		if t, ok := merged[c.Child]; ok && t.Component == "Text" {
			btn.Label = t.Text
		}
	}
	return btn
}
