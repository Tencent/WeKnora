package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// KnowledgeProfileMaxTopics caps how many topic keywords one document keeps.
	KnowledgeProfileMaxTopics = 5
	// knowledgeProfileMaxGistRunes bounds the one-line gist so it stays a
	// headline rather than a second summary.
	knowledgeProfileMaxGistRunes = 120
	// knowledgeProfileMaxTopicRunes bounds a single topic keyword.
	knowledgeProfileMaxTopicRunes = 40
	// knowledgeProfileMaxQuestionRunes bounds the typical question.
	knowledgeProfileMaxQuestionRunes = 160
	// knowledgeProfileMaxDocTypeRunes bounds the document type label.
	knowledgeProfileMaxDocTypeRunes = 40
)

// KnowledgeProfile is the structured, per-document artifact produced next to
// the free-text summary. It is deliberately tiny: a headline, a handful of
// topic keywords, a document type and one question the document can answer.
// Knowledge-base level descriptions are derived from these rows, so every
// field here is something an aggregation can count or sample.
type KnowledgeProfile struct {
	// Gist is a one-line statement of what the document is about.
	Gist string `json:"gist,omitempty"`
	// Topics lists 3-5 short topic keywords.
	Topics []string `json:"topics,omitempty"`
	// DocType is a coarse label such as "user manual", "meeting notes" or "contract".
	DocType string `json:"doc_type,omitempty"`
	// TypicalQuestion is one natural-language question this document answers.
	TypicalQuestion string `json:"typical_question,omitempty"`
}

// Value implements driver.Valuer.
func (p KnowledgeProfile) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Scan implements sql.Scanner.
func (p *KnowledgeProfile) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("unsupported knowledge profile column type")
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, p)
}

// Clone returns an independent copy (nil-safe) so a cloned document does
// not share the topic slice with its source.
func (p *KnowledgeProfile) Clone() *KnowledgeProfile {
	if p == nil {
		return nil
	}
	out := *p
	if p.Topics != nil {
		out.Topics = append([]string(nil), p.Topics...)
	}
	return &out
}

// IsEmpty reports whether the profile carries no usable field.
func (p *KnowledgeProfile) IsEmpty() bool {
	if p == nil {
		return true
	}
	return strings.TrimSpace(p.Gist) == "" &&
		len(p.Topics) == 0 &&
		strings.TrimSpace(p.DocType) == "" &&
		strings.TrimSpace(p.TypicalQuestion) == ""
}

// Normalize trims, deduplicates and bounds every field so model output of
// arbitrary shape becomes a stable, small record. It returns the receiver so
// callers can chain it, and nil when nothing survives.
func (p *KnowledgeProfile) Normalize() *KnowledgeProfile {
	if p == nil {
		return nil
	}
	p.Gist = truncateRunes(collapseWhitespace(p.Gist), knowledgeProfileMaxGistRunes)
	p.DocType = truncateRunes(collapseWhitespace(p.DocType), knowledgeProfileMaxDocTypeRunes)
	p.TypicalQuestion = truncateRunes(collapseWhitespace(p.TypicalQuestion), knowledgeProfileMaxQuestionRunes)
	p.Topics = NormalizeTopicList(p.Topics, KnowledgeProfileMaxTopics)
	if p.IsEmpty() {
		return nil
	}
	return p
}

// NormalizeTopicList trims, bounds and deduplicates topic keywords while
// preserving the first spelling seen for each normalized key.
func NormalizeTopicList(topics []string, limit int) []string {
	if len(topics) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(topics))
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		display := truncateRunes(collapseWhitespace(topic), knowledgeProfileMaxTopicRunes)
		key := TopicKey(display)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, display)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TopicKey folds a topic keyword into the key used for counting across
// documents: lower-cased, whitespace-collapsed, punctuation around the term
// removed. "Kubernetes " and "kubernetes." count as one topic; "K8s" stays
// separate and is left for the model to merge when it writes the
// knowledge-base gist.
func TopicKey(topic string) string {
	return trimTopicEdges(strings.ToLower(collapseWhitespace(topic)))
}

// topicNameMarks are the punctuation characters a topic's name can end or
// start with. C#, F# and R# are languages, not a letter followed by formatting,
// so trimming the mark folds them into C, F and R: the two labels would share
// one key, sharing a count, and NormalizeTopicList would keep only the first.
// Underscores are deliberately absent: Markdown emphasis around a keyword
// ("_Kubernetes_") is formatting and must still be trimmed.
const topicNameMarks = "#@"

// trimTopicEdges removes punctuation that only wraps a term, leaving a mark
// that is attached to the word it belongs to ("C#", "@handle"). It is the
// edge-trimming half of TopicKey, kept separate so the rule that a name mark
// survives is stated and tested in one place.
func trimTopicEdges(s string) string {
	s = strings.TrimSpace(s)
	for s != "" {
		r, size := utf8.DecodeLastRuneInString(s)
		if !unicode.IsPunct(r) {
			break
		}
		if strings.ContainsRune(topicNameMarks, r) && endsWithWord(s[:len(s)-size]) {
			break
		}
		s = strings.TrimSpace(s[:len(s)-size])
	}
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if !unicode.IsPunct(r) {
			break
		}
		if strings.ContainsRune(topicNameMarks, r) && startsWithWord(s[size:]) {
			break
		}
		s = strings.TrimSpace(s[size:])
	}
	return s
}

func endsWithWord(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func startsWithWord(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit]))
}
