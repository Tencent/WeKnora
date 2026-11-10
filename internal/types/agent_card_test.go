package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// a2uiTestContent is a realistic assistant message: prose, then one ```a2ui
// fence holding a createSurface + updateComponents pair. The component tree
// has two clickable buttons (noop and a forbidden drop_database) plus a Text
// label each, and a root Card. Double-quoted strings on purpose: a raw
// backtick string cannot contain the ``` fence markers.
const a2uiTestContent = "前置说明\n```a2ui\n" +
	`[{"version":"v0.9","createSurface":{"surfaceId":"s1",` +
	`"catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
	`{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
	`{"id":"root","component":"Card","child":"col1"},` +
	`{"id":"col1","component":"Column","children":["t1","go_lbl","go","bad_lbl","bad"]},` +
	`{"id":"t1","component":"Text","text":"标题","variant":"h2"},` +
	`{"id":"go_lbl","component":"Text","text":"知道了"},` +
	`{"id":"go","component":"Button","child":"go_lbl","variant":"primary","action":{"event":{"name":"noop"}}},` +
	`{"id":"bad_lbl","component":"Text","text":"删库"},` +
	`{"id":"bad","component":"Button","child":"bad_lbl","action":{"event":{"name":"drop_database"}}}` +
	`]}}]` + "\n```\n后置说明"

// TestExtractA2UIMessagesParsesOfficialFrames pins that the fence body (a JSON
// array of frames) decodes into typed messages and that a wrong-version frame
// is dropped while a correct one survives.
func TestExtractA2UIMessagesParsesOfficialFrames(t *testing.T) {
	msgs := ExtractA2UIMessages(a2uiTestContent)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(msgs))
	}
	if msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "s1" {
		t.Fatalf("frame[0] createSurface wrong: %+v", msgs[0])
	}
	uc := msgs[1].UpdateComponents
	if uc == nil || uc.SurfaceID != "s1" || len(uc.Components) != 7 {
		t.Fatalf("frame[1] updateComponents wrong: %+v", msgs[1])
	}
}

// TestExtractA2UIMessagesDropsWrongVersion verifies a frame that fails JSON
// decode or carries a non-v0.9 version is skipped rather than panicking.
func TestExtractA2UIMessagesDropsWrongVersion(t *testing.T) {
	content := "```a2ui\n" +
		`[{"version":"v0.8","createSurface":{"surfaceId":"old"}},` +
		`{"version":"v0.9","createSurface":{"surfaceId":"ok"}}]` + "\n```\n"
	msgs := ExtractA2UIMessages(content)
	if len(msgs) != 1 || msgs[0].CreateSurface.SurfaceID != "ok" {
		t.Fatalf("wrong-version frame not dropped: %+v", msgs)
	}
	// Malformed fence body: the whole block is skipped, no panic, no partial.
	bad := "```a2ui\n{not json\n```\n"
	if got := ExtractA2UIMessages(bad); len(got) != 0 {
		t.Fatalf("malformed fence produced %d frames", len(got))
	}
	// No fence at all.
	if got := ExtractA2UIMessages("no card here"); len(got) != 0 {
		t.Fatalf("no fence produced %d frames", len(got))
	}
}

// TestFindA2UIButtonResolvesEventAndLabel pins that the re-derivation reads
// the action name and the label (from the child Text component) off the
// persisted tree, and that a non-Button or an unknown id is nil.
func TestFindA2UIButtonResolvesEventAndLabel(t *testing.T) {
	btn := FindA2UIButton(a2uiTestContent, "s1", "go")
	if btn == nil {
		t.Fatal("expected to locate the 'go' button")
	}
	if btn.ActionName != "noop" {
		t.Errorf("ActionName = %q, want noop", btn.ActionName)
	}
	if btn.Label != "知道了" {
		t.Errorf("Label = %q, want 知道了 (resolved from child Text)", btn.Label)
	}
	// A non-Button component is never a clickable action.
	if got := FindA2UIButton(a2uiTestContent, "s1", "t1"); got != nil {
		t.Errorf("Text component resolved as button: %+v", got)
	}
	// Unknown surface / component → nil (the handler's unresolved path).
	if got := FindA2UIButton(a2uiTestContent, "s2", "go"); got != nil {
		t.Errorf("unknown surface resolved: %+v", got)
	}
	if got := FindA2UIButton(a2uiTestContent, "s1", "ghost"); got != nil {
		t.Errorf("unknown component resolved: %+v", got)
	}
}

// TestFindA2UIButtonMergeLastWriteWins pins the official updateComponents
// merge semantics: when two updateComponents frames for the same surface
// redefine the same component id, the LATER frame's definition wins — so a
// tampered earlier frame cannot pin a different action name than the one the
// model finally emitted.
func TestFindA2UIButtonMergeLastWriteWins(t *testing.T) {
	content := "```a2ui\n" +
		`[{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[{"id":"b","component":"Button",` +
		`"child":"l",` +
		`"action":{"event":{"name":"drop_database"}}}]}}` +
		`,{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[{"id":"b","component":"Button",` +
		`"child":"l","action":{"event":{"name":"noop"}}},` +
		`{"id":"l","component":"Text",` +
		`"text":"ok"}]}}]` +
		"\n```\n"
	btn := FindA2UIButton(content, "s1", "b")
	if btn == nil {
		t.Fatal("button not found")
	}
	if btn.ActionName != "noop" {
		t.Fatalf("merge should let the later frame win; got %q, want noop", btn.ActionName)
	}
}

// TestAgentCardActionPolicyPinsWhitelistAndRoleFloor pins the two security
// predicates with literal expected values (not the shared symbols, so a
// rename drifting both sides still turns this red): v0 whitelists exactly
// "noop" and "confirm", both at the viewer floor, and any other name is
// refused by the whitelist AND would require Admin on the role check.
func TestAgentCardActionPolicyPinsWhitelistAndRoleFloor(t *testing.T) {
	cases := []struct {
		name         string
		allowed      bool
		requiredRole string
	}{
		{"noop", true, "viewer"},
		{"confirm", true, "viewer"},
		{"noop ", false, "admin"}, // not trimmed before lookup → miss
		{"NOOP", false, "admin"},  // case-sensitive
		{"drop_database", false, "admin"},
		{"", false, "admin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AgentCardActionAllowed(c.name); got != c.allowed {
				t.Errorf("AgentCardActionAllowed(%q) = %v, want %v", c.name, got, c.allowed)
			}
			if got := string(AgentCardRequiredRole(c.name)); got != c.requiredRole {
				t.Errorf("AgentCardRequiredRole(%q) = %q, want %q", c.name, got, c.requiredRole)
			}
		})
	}
}

// TestSetAgentCardActionPolicyForTestRestores pins the test hook: the swap is
// visible to both predicates and the restore brings back the shipped policy.
// Run sequentially (no t.Parallel) because the swap is a package global.
func TestSetAgentCardActionPolicyForTestRestores(t *testing.T) {
	restore := SetAgentCardActionPolicyForTest(map[string]TenantRole{"reset_db": TenantRoleAdmin})
	if !AgentCardActionAllowed("reset_db") {
		t.Fatal("injected policy not visible to the whitelist")
	}
	if got := string(AgentCardRequiredRole("reset_db")); got != "admin" {
		t.Fatalf("injected role = %q, want admin", got)
	}
	restore()
	if AgentCardActionAllowed("reset_db") {
		t.Fatal("restore did not bring back the shipped policy")
	}
	if !AgentCardActionAllowed("noop") {
		t.Fatal("post-restore noop is no longer allowed")
	}
}

// The tag literals below are spelled out, not taken from A2UITagOpen/A2UITagClose:
// the extractor, the teaching and this expectation sharing one symbol would keep
// every test green while all three said something the official renderer ignores.
const (
	tagOpenLit  = "<a2ui-json>"
	tagCloseLit = "</a2ui-json>"
	// a2uiTestFrames is the payload used by the fence tests above, verbatim, so
	// "the tag form yields the same frames as the fence form" compares forms and
	// not two different cards.
	a2uiTestFrames = `[{"version":"v0.9","createSurface":{"surfaceId":"s1",` +
		`"catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
		`{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
		`{"id":"root","component":"Card","child":"col1"},` +
		`{"id":"col1","component":"Column","children":["t1","go_lbl","go"]},` +
		`{"id":"t1","component":"Text","text":"标题","variant":"h2"},` +
		`{"id":"go_lbl","component":"Text","text":"知道了"},` +
		`{"id":"go","component":"Button","child":"go_lbl","variant":"primary","action":{"event":{"name":"noop"}}}` +
		`]}}]`
)

// TestExtractA2UIMessagesAcceptsOfficialTagForm is the positive half of the
// official format: the payload between the tags decodes into the same frames the
// fence form gives, the button is re-derivable from it, and the tolerances the
// official parser has (case-insensitive tag, attributes on the open tag, a
// markdown fence wrapped around the payload — blocks.py
// _LEADING_FENCE_RE/_TRAILING_FENCE_RE) are tolerances we share, because a card
// we reject is a card the official renderer would have shown.
func TestExtractA2UIMessagesAcceptsOfficialTagForm(t *testing.T) {
	content := "前置说明\n" + tagOpenLit + "\n" + a2uiTestFrames + "\n" + tagCloseLit + "\n后置说明"
	msgs := ExtractA2UIMessages(content)
	if len(msgs) != 2 {
		t.Fatalf("tag form yielded %d frames, want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "s1" {
		t.Fatalf("frame[0] createSurface wrong: %+v", msgs[0])
	}
	if uc := msgs[1].UpdateComponents; uc == nil || uc.SurfaceID != "s1" || len(uc.Components) != 5 {
		t.Fatalf("frame[1] updateComponents wrong: %+v", msgs[1])
	}
	btn := FindA2UIButton(content, "s1", "go")
	if btn == nil || btn.ActionName != "noop" || btn.Label != "知道了" {
		t.Fatalf("button not re-derivable from the tag form: %+v", btn)
	}

	// Official tolerance: uppercase tag + attribute on the open tag.
	tolerant := `<A2UI-JSON data-source="weknora">` + a2uiTestFrames + `</A2UI-JSON>`
	if got := ExtractA2UIMessages(tolerant); len(got) != 2 {
		t.Fatalf("uppercase/attributed tag yielded %d frames, want 2", len(got))
	}

	// Official tolerance: a fence wrapped around the payload inside the tags.
	fenced := tagOpenLit + "\n```json\n" + a2uiTestFrames + "\n```\n" + tagCloseLit
	if got := ExtractA2UIMessages(fenced); len(got) != 2 {
		t.Fatalf("fence inside the tags yielded %d frames, want 2 (official parser strips it)", len(got))
	}
}

// TestExtractA2UIMessagesTagFormAnchors keeps the assertions above from being
// satisfied by "any text yields frames": the same payload with the wrapper
// removed yields nothing, a close tag with no opener yields nothing, prose that
// merely mentions the tag name yields nothing — and the fence form still yields
// its frames, which is what pins the tag work as additive rather than a swap
// that broke stored messages.
func TestExtractA2UIMessagesTagFormAnchors(t *testing.T) {
	if got := ExtractA2UIMessages(a2uiTestFrames); len(got) != 0 {
		t.Fatalf("bare array with no wrapper yielded %d frames, want 0: the wrapper is not load-bearing", len(got))
	}
	if got := ExtractA2UIMessages(a2uiTestFrames + "\n" + tagCloseLit); len(got) != 0 {
		t.Fatalf("payload with only a closing tag yielded %d frames, want 0", len(got))
	}
	if got := ExtractA2UIMessages("这段回答里提到了 " + tagOpenLit + " 标签，但没有卡片内容。"); len(got) != 0 {
		t.Fatalf("prose mentioning the tag yielded %d frames, want 0", len(got))
	}
	if got := ExtractA2UIMessages(a2uiTestContent); len(got) != 2 {
		t.Fatalf("legacy fence yielded %d frames, want 2 (stored messages must keep replaying)", len(got))
	}
}

// TestExtractA2UIMessagesDoesNotDoubleCountNestedBlocks covers the overlap rule:
// a tag block sitting inside a fence body is JSON string content of that fence,
// not a second card, and a fence inside a tagged payload is already covered by
// that payload. Accepting both would emit the same frames twice and double the
// audit surface, so the outer block wins and the inner span is dropped.
func TestExtractA2UIMessagesDoesNotDoubleCountNestedBlocks(t *testing.T) {
	// Tag block inside a fence body: the fence payload is not JSON, the tag
	// payload is, so the tag wins — the official reading (a fence is noise).
	nested := "```a2ui\n" + tagOpenLit + a2uiTestFrames + tagCloseLit + "\n```\n"
	if got := ExtractA2UIMessages(nested); len(got) != 2 {
		t.Fatalf("fence with a nested tag block yielded %d frames, want 2 (counted once)", len(got))
	}
	// The other nesting direction, in both accepted forms: a card whose JSON
	// quotes a full tag pair inside a string value (a card that documents the
	// format — WeKnora answering "how do I emit an A2UI card?"). The quoted
	// close tag must NOT end the block: blocks.py BlockScanner states "A tag
	// written inside a JSON string does not count". One card, s1; the quoted
	// surface id "x" must never exist; the literals ride along as data.
	const quotedInner = `[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"x\"}}]`
	quotingFence := "```a2ui\n" +
		`[{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
		`{"id":"root","component":"Text","text":"写卡片要写 ` + tagOpenLit + quotedInner + tagCloseLit + `"}]}}]` +
		"\n```\n"
	qmsgs := ExtractA2UIMessages(quotingFence)
	if len(qmsgs) != 1 || qmsgs[0].UpdateComponents == nil || qmsgs[0].UpdateComponents.SurfaceID != "s1" {
		t.Fatalf("fence quoting the tag literals yielded %+v, want the fence's single s1 frame", qmsgs)
	}
	if FindA2UIButton(quotingFence, "x", "root") != nil {
		t.Fatal("the quoted tag literals were read as a second card")
	}
	quotingTag := tagOpenLit +
		`[{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
		`{"id":"root","component":"Text","text":"写卡片要写 ` + tagOpenLit + quotedInner + tagCloseLit + `"}]}}]` +
		tagCloseLit
	tmsgs := ExtractA2UIMessages(quotingTag)
	if len(tmsgs) != 1 || tmsgs[0].UpdateComponents == nil || len(tmsgs[0].UpdateComponents.Components) != 1 {
		t.Fatalf("tag form quoting the tag literals yielded %+v, want one frame with one component", tmsgs)
	}
	txt := tmsgs[0].UpdateComponents.Components[0].Text
	if !strings.Contains(txt, tagOpenLit) || !strings.Contains(txt, "surfaceId") {
		t.Errorf("quoted literals did not ride along as payload data: %q", txt)
	}
	// Fence inside a tagged payload — asserted in the tag test above; here the
	// two-block case that must NOT collapse: two sibling blocks in one message
	// are two cards, in document order.
	two := tagOpenLit + `[{"version":"v0.9","createSurface":{"surfaceId":"a"}}]` + tagCloseLit +
		"\nmid\n```a2ui\n" + `[{"version":"v0.9","createSurface":{"surfaceId":"b"}}]` + "\n```\n"
	msgs := ExtractA2UIMessages(two)
	if len(msgs) != 2 || msgs[0].CreateSurface.SurfaceID != "a" || msgs[1].CreateSurface.SurfaceID != "b" {
		t.Fatalf("two sibling blocks yielded %d frames out of order: %+v", len(msgs), msgs)
	}
}

// TestFindA2UIButtonCarriesEventContext pins that the resolved button hands
// back the PERSISTED action.event.context: the confirm executor reads its
// follow-up instruction from here (never the client request body), so a
// regression that drops the field must turn this red before it can starve an
// executor. Positive and negative arms share one fixture; the no-context
// expectation is a literal (nil), not a re-read of the implementation.
func TestFindA2UIButtonCarriesEventContext(t *testing.T) {
	content := "确认卡\n```a2ui\n" +
		`[{"version":"v0.9","createSurface":{"surfaceId":"s3",` +
		`"catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
		`{"version":"v0.9","updateComponents":{"surfaceId":"s3","components":[` +
		`{"id":"root","component":"Card","child":"col1"},` +
		`{"id":"col1","component":"Column","children":["ok_lbl","ok","plain_lbl","plain"]},` +
		`{"id":"ok_lbl","component":"Text","text":"确定"},` +
		`{"id":"ok","component":"Button","child":"ok_lbl","variant":"primary",` +
		`"action":{"event":{"name":"confirm","context":{"message":"用户已确认，请按方案继续执行后续操作"}}}},` +
		`{"id":"plain_lbl","component":"Text","text":"知道了"},` +
		`{"id":"plain","component":"Button","child":"plain_lbl","action":{"event":{"name":"noop"}}}` +
		`]}}]` + "\n```\n"

	btn := FindA2UIButton(content, "s3", "ok")
	if btn == nil {
		t.Fatal("confirm button not found")
	}
	raw, ok := btn.Context["message"]
	if !ok {
		t.Fatal("btn.Context is missing the persisted \"message\" key")
	}
	var msg string
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("context.message is not a JSON string: %v", err)
	}
	if msg != "用户已确认，请按方案继续执行后续操作" {
		t.Fatalf("context.message = %q", msg)
	}

	plain := FindA2UIButton(content, "s3", "plain")
	if plain == nil {
		t.Fatal("noop button not found")
	}
	if plain.Context != nil {
		t.Fatalf("a button without context must resolve to a nil Context, got %v", plain.Context)
	}
}

const a2uiFormCardForTypes = "<a2ui-json>\n" +
	`[{"version":"v0.9","createSurface":{"surfaceId":"s1",` +
	`"catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
	`{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
	`{"id":"root","component":"Card","child":"col"},` +
	`{"id":"col","component":"Column","children":["f_name","f_pick","f_amb","btn"]},` +
	`{"id":"f_name","component":"TextField","label":"姓名","value":{"path":"/name"}},` +
	`{"id":"f_pick","component":"ChoicePicker","label":{"path":"/lbl"},` +
	`"options":[{"label":"甲","value":"a"}],"value":{"path":"/room"}},` +
	`{"id":"f_amb","component":"TextField","label":"备注","value":"写字没处去"},` +
	`{"id":"btn_lbl","component":"Text","text":"提交"},` +
	`{"id":"btn","component":"Button","child":"btn_lbl","action":{"event":{"name":"confirm","context":{` +
	`"message":"继续办理。","name":{"path":"/name"},"room":{"path":"/room"},"ghost":{"path":"/ghost"}}}}}` +
	`]}}]` + "\n</a2ui-json>"

func TestFindA2UIButtonCollectsFormLabelsAndBoundPaths(t *testing.T) {
	btn := FindA2UIButton(a2uiFormCardForTypes, "s1", "btn")
	if btn == nil {
		t.Fatal("form button s1/b1 not re-derivable")
	}
	wantPaths := []string{"/name", "/room"}
	if got, want := fmt.Sprint(btn.BoundInputPaths), fmt.Sprint(wantPaths); got != want {
		t.Errorf("BoundInputPaths = %v, want %v (dedup+sorted; 字面量 value 与别个 surface 的输入都不收)",
			btn.BoundInputPaths, wantPaths)
	}
	if len(btn.FieldLabels) != 1 {
		t.Fatalf("FieldLabels = %v, want exactly 1 entry", btn.FieldLabels)
	}
	if btn.FieldLabels["name"] != "姓名" {
		t.Errorf(`FieldLabels["name"] = %q, want %q`, btn.FieldLabels["name"], "姓名")
	}
}

func TestFindA2UIButtonLegacyCardCollectsNothing(t *testing.T) {
	content := "<a2ui-json>\n" +
		`[{"version":"v0.9","createSurface":{"surfaceId":"s1",` +
		`"catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
		`{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
		`{"id":"root","component":"Card","child":"b1_lbl"},` +
		`{"id":"b1_lbl","component":"Text","text":"确定"},` +
		`{"id":"b1","component":"Button","child":"b1_lbl",` +
		`"action":{"event":{"name":"confirm","context":{"message":"继续。"}}}}]}}]` +
		"\n</a2ui-json>"
	btn := FindA2UIButton(content, "s1", "b1")
	if btn == nil {
		t.Fatal("legacy confirm button not re-derivable")
	}
	if len(btn.BoundInputPaths) != 0 {
		t.Errorf("BoundInputPaths = %v, want empty for a card without inputs", btn.BoundInputPaths)
	}
	if len(btn.FieldLabels) != 0 {
		t.Errorf("FieldLabels = %v, want empty for a card without bindings", btn.FieldLabels)
	}
}
