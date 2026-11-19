package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// Form-input hard caps.
// The teaching cap (≤8 fields) is looser than the server cap on purpose.
const (
	a2uiFormMaxStringBytes = 4096  // per string value, CJK counted in bytes
	a2uiFormMaxArrayItems  = 32    // per bound scalar array
	a2uiFormMaxTotalBytes  = 16384 // all bound keys, JSON-marshalled together
	a2uiFormMaxBoundKeys   = 20    // spec-side: binding keys on one button
)

// a2uiFormUnfilledRaw is the server-authored JSON string "未填写" that stands
// in for a bound field the user left blank. Every composed form line
// therefore states a value, so the follow-up run can never mistake a blank
// for a hole to fill. Tests pin the literal, never this variable.
var a2uiFormUnfilledRaw = json.RawMessage(`"未填写"`)

// Replay denial reasons, recorded verbatim in the audit denial row's
// details.reason. The handler forwards them untouched; tests compare
// literals (never these constants — shared-source pins are vacuous).
const (
	A2UIFormRejectValueUnsupported = "form_value_unsupported"
	A2UIFormRejectPathUnbound      = "form_path_unbound"
	A2UIFormRejectKeysMismatch     = "form_keys_mismatch"
	A2UIFormRejectLiteralMismatch  = "form_literal_mismatch"
	A2UIFormRejectValueInvalid     = "form_value_invalid"
	A2UIFormRejectTooLarge         = "form_too_large"
)

// a2uiSpecKind classifies one persisted context value into exactly one of
// "call" (function-derived: an object carrying "call" — refused on sight),
// "path" (the official single-property binding {"path":"…"}), or "literal"
// (everything else, including {"path":123}: it must simply echo back).
func a2uiSpecKind(raw json.RawMessage) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "literal"
	}
	if _, isCall := obj["call"]; isCall {
		return "call"
	}
	if _, ok := obj["path"]; ok && len(obj) == 1 {
		if _, binding := a2uiBindingPath(raw); binding {
			return "path"
		}
	}
	return "literal"
}

// ReplayA2UIForm replays a submitted action context against the button's
// PERSISTED context spec — it extends the rule that anything the client
// claims is vetoed by persisted content, rather than relaxing it:
// the spec decides which keys may carry user input at all (only {"path":…}
// bindings whose path some input component on the same surface declared),
// every literal (message included) is read back from the persisted side and
// the submission must echo it exactly, and the accepted binding values flow
// to exactly one destination — the next user message.
//
// Check order is fixed and the first hit wins:
//
//	1 form_value_unsupported  spec carries a {call} value (structural)
//	2 form_path_unbound       a binding path no input component declared
//	3 form_keys_mismatch      a submitted key the spec never declared, or a
//	                          persisted literal DROPPED from the submission.
//	                          An absent BOUND key is not a mismatch: renderers
//	                          drop fields the user never touched, so "absent"
//	                          is the normal shape of "left blank" — backfilled
//	                          below as the unfilled literal, so every
//	                          composed line states a value and the model
//	                          can never hallucinate a blank
//	4 form_literal_mismatch   a literal differs semantically (Unmarshal+
//	                          DeepEqual — formatting drift like {"a":1} vs
//	                          {"a": 1} must PASS)
//	5 form_value_invalid      bound value not a string/number/bool or a scalar
//	                          array (≤32 items); nested objects and null refuse
//	6 form_too_large          caps: string ≤4096B, total bound payload
//	                          ≤16384B, ≤20 bound keys
//
// Spec-side loops run in sorted key order so the reason never depends on map
// luck. An empty/absent submission replays a specless or literal-only card
// unchanged — the legacy pure-confirm path keeps flowing through this function
// (empty spec + empty submission ⇒ pass), and a legacy card forged INTO with
// extra keys is refused here, not silently ignored.
//
// On success the returned map carries ONLY the accepted binding values
// (raw JSON as submitted, caps verified); literals are deliberately absent —
// the composer must never have a chance to read the client's copy of anything
// the persisted side already decides.
func ReplayA2UIForm(spec map[string]json.RawMessage, submitted json.RawMessage,
	boundPaths []string,
) (map[string]json.RawMessage, string) {
	bound := make(map[string]bool, len(boundPaths))
	for _, p := range boundPaths {
		bound[p] = true
	}
	specKeys := sortedA2UIContextKeys(spec)

	kinds := make(map[string]string, len(spec))
	boundCount := 0
	for _, k := range specKeys {
		kind := a2uiSpecKind(spec[k])
		kinds[k] = kind
		switch kind {
		case "call":
			return nil, A2UIFormRejectValueUnsupported
		case "path":
			p, _ := a2uiBindingPath(spec[k])
			if !bound[p] {
				return nil, A2UIFormRejectPathUnbound
			}
			boundCount++
		}
	}

	sub := map[string]json.RawMessage{}
	if trimmed := bytes.TrimSpace(submitted); len(trimmed) > 0 && string(trimmed) != "null" {
		if err := json.Unmarshal(trimmed, &sub); err != nil {
			return nil, A2UIFormRejectKeysMismatch
		}
	}

	// Rule 3, part 1: every submitted key must be declared by the spec — an
	// extra or forged key is the client claiming input the model never
	// offered. (Deliberately NOT a length comparison: absent bound keys are
	// legal and backfilled below.)
	for k := range sub {
		if _, ok := spec[k]; !ok {
			return nil, A2UIFormRejectKeysMismatch
		}
	}
	// Rule 3, part 2: every persisted literal must be echoed. Dropping one
	// is the client presenting a different card than the one it was shown;
	// presence is checked here (rule 3) so a dropped literal reports
	// form_keys_mismatch, leaving rule 4 for present-but-altered literals.
	for _, k := range specKeys {
		if kinds[k] == "literal" {
			if _, ok := sub[k]; !ok {
				return nil, A2UIFormRejectKeysMismatch
			}
		}
	}

	for _, k := range specKeys {
		if kinds[k] != "literal" {
			continue
		}
		var sv, cv any
		if json.Unmarshal(spec[k], &sv) != nil || json.Unmarshal(sub[k], &cv) != nil {
			return nil, A2UIFormRejectLiteralMismatch
		}
		if !reflect.DeepEqual(sv, cv) {
			return nil, A2UIFormRejectLiteralMismatch
		}
	}

	values := make(map[string]json.RawMessage, boundCount)
	for _, k := range specKeys {
		if kinds[k] != "path" {
			continue
		}
		raw, ok := sub[k]
		if !ok {
			// Absent bound key = the user left the field blank (renderers
			// do not transmit untouched paths). Not forgery, just empty —
			// state it explicitly instead of refusing the whole click.
			values[k] = a2uiFormUnfilledRaw
			continue
		}
		if !a2uiFormValueShapeOK(raw) {
			return nil, A2UIFormRejectValueInvalid
		}
		values[k] = a2uiFormBackfillEmpty(raw)
	}

	if boundCount > a2uiFormMaxBoundKeys {
		return nil, A2UIFormRejectTooLarge
	}
	total, err := json.Marshal(values)
	if err != nil || len(total) > a2uiFormMaxTotalBytes {
		return nil, A2UIFormRejectTooLarge
	}
	for _, k := range specKeys {
		if kinds[k] == "path" && !a2uiFormValueSizeOK(values[k]) {
			return nil, A2UIFormRejectTooLarge
		}
	}
	return values, ""
}

// a2uiFormValueShapeOK accepts JSON scalars (string/number/bool) and arrays of
// scalars up to a2uiFormMaxArrayItems. Nested objects, null and oversized
// arrays are SHAPE violations regardless of size (table row 5).
func a2uiFormValueShapeOK(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case string, float64, bool:
		return true
	case []any:
		if len(t) > a2uiFormMaxArrayItems {
			return false
		}
		for _, it := range t {
			switch it.(type) {
			case string, float64, bool:
			default:
				return false
			}
		}
		return true
	}
	return false
}

// a2uiFormValueSizeOK enforces the per-string byte cap on an already
// shape-valid value (every string item in an array counts; table row 6).
func a2uiFormValueSizeOK(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case string:
		return len(t) <= a2uiFormMaxStringBytes
	case []any:
		for _, it := range t {
			if s, ok := it.(string); ok && len(s) > a2uiFormMaxStringBytes {
				return false
			}
		}
	}
	return true
}

// a2uiFormBackfillEmpty turns a PRESENT-but-empty submitted value ("" or [])
// into the unfilled placeholder, so a user who typed nothing produces the
// same stated line as one whose field the renderer dropped entirely. Only
// the two empty shapes are replaced: 0 and false are values the user chose,
// and every other shape passes through byte-untouched.
func a2uiFormBackfillEmpty(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return a2uiFormUnfilledRaw
		}
	case []any:
		if len(t) == 0 {
			return a2uiFormUnfilledRaw
		}
	}
	return raw
}

// SortedA2UIFormKeys is the ONE deterministic order over replayed values: the
// composer's lines and the audit's form_keys both come from this same sort,
// so the two can never disagree about which keys were accepted.
func SortedA2UIFormKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ComposeConfirmFollowUp renders the confirm follow-up text: the persisted
// instruction plus — only when the replay accepted bound values — a fixed
// header line and one "- label: value" line per accepted key, in the ONE
// deterministic key order (SortedA2UIFormKeys; the audit's form_keys comes
// from the same sort). No accepted values ⇒ the result is byte-identical to
// the pre-form confirm text (the regression anchor the pure-confirm tests
// pin). labels is A2uiResolvedButton.FieldLabels; a key without a literal
// label falls back to the key name.
//
// Header text and the label rule are a fixed format: everything
// typeset here comes from persisted content except the accepted values
// themselves, which are the ONLY submitted content that survives replay —
// and they land in exactly one place: this user message.
func ComposeConfirmFollowUp(message string, values map[string]json.RawMessage, labels map[string]string) string {
	keys := SortedA2UIFormKeys(values)
	if len(keys) == 0 {
		return message
	}
	var b strings.Builder
	b.WriteString(message)
	b.WriteString("\n\n用户提交的表单：\n")
	for _, k := range keys {
		label := labels[k]
		if label == "" {
			label = k
		}
		b.WriteString("- ")
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(a2uiFormValueText(values[k]))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// a2uiFormValueText renders one replay-valid value: strings raw (NUL
// stripped, newlines kept), numbers/bools as the JSON literals as submitted,
// arrays compacted. Anything else returns "" — unreachable through replay
// (table row 5 refused it), the empty fallback is belt-and-close, never an
// echo of raw bytes we did not validate.
func a2uiFormValueText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.ReplaceAll(s, "\x00", "")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return ""
	}
	if _, isObj := v.(map[string]any); isObj {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return ""
	}
	return buf.String()
}
