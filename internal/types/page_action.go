package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// AgentPromptKindPageAction asks the user's browser to read or fill the
// WeKnora page beside the assistant. The browser answers on its own; no card
// is shown.
const AgentPromptKindPageAction = "page_action"

// Actions of a page_action prompt.
const (
	PageActionRead     = "read"
	PageActionFill     = "fill"
	PageActionNavigate = "navigate"
	// PageActionSelect opens a tab the page is already showing. It changes
	// which panel is visible and nothing else.
	PageActionSelect = "select"
)

// Operations of a PageTextEdit.
const (
	PageEditReplace          = "replace"
	PageEditInsertBefore     = "insert_before"
	PageEditInsertAfter      = "insert_after"
	PageEditReplaceSelection = "replace_selection"
	PageEditAppend           = "append"
)

// Limits on a page action. The model writes the fill, the browser writes the
// snapshot; both are bounded so a prompt row and a tool result stay small.
const (
	PageReadMaxLineCount     = 400
	PageFillMaxChanges       = 20
	PageFillMaxEdits         = 50
	PageFillMaxOptions       = 50
	PageFillMaxTextRunes     = 50000
	PageFillMaxTotalRunes    = 100000
	PageSnapshotMaxRunes     = 60000
	PageActionMaxLabelRunes  = 200
	PageActionMaxDetailRunes = 2000
	pageActionMaxResponse    = 512 << 10
)

var pageRefRE = regexp.MustCompile(`^e[1-9][0-9]{0,6}$`)

// PageTextEdit is one change to a text field, located by text rather than
// position so it still lands after the user typed elsewhere.
type PageTextEdit struct {
	Op string `json:"op"`
	// OldText is the text a replace swaps out; it must occur exactly once.
	OldText string `json:"old_text,omitempty"`
	// Anchor is the text an insert goes before or after; it must occur
	// exactly once.
	Anchor string `json:"anchor,omitempty"`
	Text   string `json:"text"`
}

// PageFillChange sets one field: Value replaces it, Edits patch its text.
type PageFillChange struct {
	Ref   string          `json:"ref"`
	Value json.RawMessage `json:"value,omitempty"`
	Edits []PageTextEdit  `json:"edits,omitempty"`
}

// PageNavigateTarget is a checked open_page link; the browser turns it into
// a location the same way it does for the link's button.
type PageNavigateTarget struct {
	Page            string `json:"page"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	KnowledgeID     string `json:"knowledge_id,omitempty"`
	WikiSlug        string `json:"wiki_slug,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	AutomationID    string `json:"automation_id,omitempty"`
	Section         string `json:"section,omitempty"`
	// Panel is a tab inside a settings section, e.g. system-global's users.
	Panel        string `json:"panel,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	MCPServiceID string `json:"mcp_service_id,omitempty"`
}

// PageActionPromptRequest is the Request of an AgentPromptKindPageAction.
type PageActionPromptRequest struct {
	Action string `json:"action"`
	// Ref, on a read, returns that field's text instead of the whole page.
	Ref       string           `json:"ref,omitempty"`
	StartLine int              `json:"start_line,omitempty"`
	LineCount int              `json:"line_count,omitempty"`
	Changes   []PageFillChange `json:"changes,omitempty"`
	// Target is where a navigate goes; the page is read once it is there.
	Target *PageNavigateTarget `json:"target,omitempty"`
}

// PageFillApplied describes one field a fill changed, for the model and the
// undo card.
type PageFillApplied struct {
	Ref     string `json:"ref"`
	Label   string `json:"label,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// PageActionPromptResponse is the Response of an AgentPromptKindPageAction.
// Declined means the browser would not act, e.g. the user turned page access
// off; Error means it tried and nothing was changed.
type PageActionPromptResponse struct {
	Declined bool   `json:"declined,omitempty"`
	Error    string `json:"error,omitempty"`
	// Location names the page, e.g. its title and path.
	Location string `json:"location,omitempty"`
	// Snapshot is the page, or the requested field text, as read.
	Snapshot string            `json:"snapshot,omitempty"`
	Applied  []PageFillApplied `json:"applied,omitempty"`
	// Stayed means a navigate left the page alone because it has unsaved
	// changes.
	Stayed bool `json:"stayed,omitempty"`
	// ReturnPath is the in-app path a navigate left, for going back.
	ReturnPath string `json:"return_path,omitempty"`
}

// Validate checks a request against the page action limits.
func (r *PageActionPromptRequest) Validate() error {
	r.Ref = strings.TrimSpace(r.Ref)
	switch r.Action {
	case PageActionRead:
		if len(r.Changes) > 0 || r.Target != nil {
			return errors.New("a read takes no changes or target")
		}
		if r.Ref != "" && !pageRefRE.MatchString(r.Ref) {
			return fmt.Errorf("ref %q is not a field reference like e12", r.Ref)
		}
		if r.Ref == "" && (r.StartLine != 0 || r.LineCount != 0) {
			return errors.New("start_line and line_count need a ref")
		}
		if r.StartLine < 0 || r.LineCount < 0 || r.LineCount > PageReadMaxLineCount {
			return fmt.Errorf("start_line must be positive and line_count at most %d", PageReadMaxLineCount)
		}
		return nil
	case PageActionFill:
		if r.Ref != "" || r.StartLine != 0 || r.LineCount != 0 || r.Target != nil {
			return errors.New("a fill takes only changes")
		}
		return validatePageChanges(r.Changes)
	case PageActionNavigate:
		if r.Ref != "" || r.StartLine != 0 || r.LineCount != 0 || len(r.Changes) > 0 {
			return errors.New("a navigate takes only a target")
		}
		if r.Target == nil || r.Target.Page == "" {
			return errors.New("a navigate needs a target page")
		}
		return nil
	case PageActionSelect:
		if len(r.Changes) > 0 || r.Target != nil || r.StartLine != 0 || r.LineCount != 0 {
			return errors.New("a select takes only a ref")
		}
		if !pageRefRE.MatchString(r.Ref) {
			return fmt.Errorf("ref %q is not a reference like e12", r.Ref)
		}
		return nil
	default:
		return fmt.Errorf("unknown page action %q", r.Action)
	}
}

func validatePageChanges(changes []PageFillChange) error {
	if len(changes) == 0 {
		return errors.New("changes is empty")
	}
	if len(changes) > PageFillMaxChanges {
		return fmt.Errorf("at most %d changes per call", PageFillMaxChanges)
	}
	total := 0
	count := func(s string) error {
		n := utf8.RuneCountInString(s)
		if n > PageFillMaxTextRunes {
			return fmt.Errorf("a text exceeds %d characters", PageFillMaxTextRunes)
		}
		total += n
		return nil
	}
	seen := make(map[string]bool, len(changes))
	for i := range changes {
		c := &changes[i]
		c.Ref = strings.TrimSpace(c.Ref)
		if !pageRefRE.MatchString(c.Ref) {
			return fmt.Errorf("change %d: ref %q is not a field reference like e12", i, c.Ref)
		}
		if seen[c.Ref] {
			return fmt.Errorf("change %d: %s is changed twice; put all its edits in one change", i, c.Ref)
		}
		seen[c.Ref] = true
		hasValue := len(bytes.TrimSpace(c.Value)) > 0 && !bytes.Equal(bytes.TrimSpace(c.Value), []byte("null"))
		if hasValue == (len(c.Edits) > 0) {
			return fmt.Errorf("change %d: give either value or edits", i)
		}
		if hasValue {
			if err := checkPageValue(c.Value, count); err != nil {
				return fmt.Errorf("change %d: %w", i, err)
			}
			continue
		}
		if len(c.Edits) > PageFillMaxEdits {
			return fmt.Errorf("change %d: at most %d edits", i, PageFillMaxEdits)
		}
		for j, e := range c.Edits {
			if err := checkPageEdit(e); err != nil {
				return fmt.Errorf("change %d, edit %d: %w", i, j, err)
			}
			for _, s := range []string{e.OldText, e.Anchor, e.Text} {
				if err := count(s); err != nil {
					return fmt.Errorf("change %d, edit %d: %w", i, j, err)
				}
			}
		}
	}
	if total > PageFillMaxTotalRunes {
		return fmt.Errorf("the changes exceed %d characters in total; split them into several calls",
			PageFillMaxTotalRunes)
	}
	return nil
}

// checkPageValue accepts text, a number, a boolean, or a list of option labels.
func checkPageValue(raw json.RawMessage, count func(string) error) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("unreadable value: %w", err)
	}
	switch x := v.(type) {
	case string:
		return count(x)
	case float64, bool:
		return nil
	case []any:
		if len(x) > PageFillMaxOptions {
			return fmt.Errorf("at most %d options", PageFillMaxOptions)
		}
		for _, item := range x {
			s, ok := item.(string)
			if !ok || utf8.RuneCountInString(s) > PageActionMaxLabelRunes {
				return fmt.Errorf("a list value holds option labels of at most %d characters", PageActionMaxLabelRunes)
			}
		}
		return nil
	default:
		return errors.New("value must be text, a number, true/false or a list of option labels")
	}
}

func checkPageEdit(e PageTextEdit) error {
	switch e.Op {
	case PageEditReplace:
		if e.OldText == "" || e.Anchor != "" {
			return errors.New("replace takes old_text and text")
		}
	case PageEditInsertBefore, PageEditInsertAfter:
		if e.Anchor == "" || e.OldText != "" || e.Text == "" {
			return fmt.Errorf("%s takes anchor and text", e.Op)
		}
	case PageEditReplaceSelection, PageEditAppend:
		if e.OldText != "" || e.Anchor != "" {
			return fmt.Errorf("%s takes only text", e.Op)
		}
		if e.Op == PageEditAppend && e.Text == "" {
			return errors.New("append needs text")
		}
	default:
		return fmt.Errorf("unknown op %q", e.Op)
	}
	return nil
}

// ValidateAnswer checks a browser response against the request it answers.
func (r *PageActionPromptRequest) ValidateAnswer(a *PageActionPromptResponse) error {
	if utf8.RuneCountInString(a.Error) > PageActionMaxDetailRunes ||
		utf8.RuneCountInString(a.Location) > PageActionMaxDetailRunes {
		return errors.New("error or location is too long")
	}
	if a.Declined {
		if a.Snapshot != "" || len(a.Applied) > 0 || a.Stayed || a.ReturnPath != "" {
			return errors.New("a declined action carries no result")
		}
		return nil
	}
	if utf8.RuneCountInString(a.Snapshot) > PageSnapshotMaxRunes {
		return fmt.Errorf("snapshot exceeds %d characters", PageSnapshotMaxRunes)
	}
	if r.Action != PageActionNavigate && (a.Stayed || a.ReturnPath != "") {
		return errors.New("only a navigate stays or returns")
	}
	if r.Action == PageActionNavigate {
		if len(a.Applied) > 0 {
			return errors.New("a navigate applies nothing")
		}
		if a.Stayed && (a.Snapshot != "" || a.ReturnPath != "") {
			return errors.New("a navigate that stayed carries no result")
		}
		if a.ReturnPath != "" && !IsAppPath(a.ReturnPath) {
			return errors.New("return_path must be an in-app path")
		}
		return nil
	}
	if r.Action == PageActionRead || r.Action == PageActionSelect {
		if len(a.Applied) > 0 {
			return errors.New("this action applies nothing")
		}
		return nil
	}
	if a.Snapshot != "" {
		return errors.New("a fill returns no snapshot")
	}
	if a.Error != "" && len(a.Applied) > 0 {
		return errors.New("a failed fill applies nothing")
	}
	if len(a.Applied) > len(r.Changes) {
		return errors.New("more fields applied than requested")
	}
	for _, f := range a.Applied {
		if !pageRefRE.MatchString(f.Ref) ||
			utf8.RuneCountInString(f.Label) > PageActionMaxLabelRunes ||
			utf8.RuneCountInString(f.Summary) > PageActionMaxDetailRunes {
			return errors.New("invalid applied field")
		}
	}
	return nil
}

// IsAppPath reports whether path is a bounded path inside the app, never a
// URL or a protocol-relative one.
func IsAppPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.Contains(path, "\\") &&
		len(path) <= PageActionMaxDetailRunes && !strings.ContainsAny(path, "\r\n")
}

// ValidatePageActionResponse is the broker validator of AgentPromptKindPageAction.
func ValidatePageActionResponse(request, response json.RawMessage) error {
	if len(response) > pageActionMaxResponse {
		return fmt.Errorf("response exceeds %d bytes", pageActionMaxResponse)
	}
	var q PageActionPromptRequest
	if err := json.Unmarshal(request, &q); err != nil {
		return fmt.Errorf("unreadable page action: %w", err)
	}
	var a PageActionPromptResponse
	dec := json.NewDecoder(bytes.NewReader(response))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return err
	}
	return q.ValidateAnswer(&a)
}
