package types

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// Errors surfaced by knowledge-base description generation. They live here
// so HTTP handlers can classify them without importing the service package.
var (
	// ErrKnowledgeBaseProfileUnsupported is returned for knowledge bases that
	// do not hold document profiles (FAQ, temporary).
	ErrKnowledgeBaseProfileUnsupported = errors.New("knowledge base type does not support generated descriptions")
	// ErrKnowledgeBaseProfileModelNotConfigured is returned when neither the
	// profile config nor the knowledge base names a chat model.
	ErrKnowledgeBaseProfileModelNotConfigured = errors.New("no chat model configured for knowledge base description")
)

// Knowledge-base profile status values.
const (
	// KnowledgeBaseProfileStatusReady means the stored profile matches the
	// aggregate hash it was generated from.
	KnowledgeBaseProfileStatusReady = "ready"
	// KnowledgeBaseProfileStatusEmpty means the knowledge base had no eligible
	// documents when the profile was last computed; nothing was sent to a model.
	KnowledgeBaseProfileStatusEmpty = "empty"
	// KnowledgeBaseProfileStatusFailed records the last generation error so the
	// UI can show it. The previous gist, if any, is kept.
	KnowledgeBaseProfileStatusFailed = "failed"
)

const (
	// KnowledgeBaseProfileMaxTopics caps the merged topic list the model returns.
	KnowledgeBaseProfileMaxTopics = 10
	// KnowledgeBaseProfileMaxQuestions caps the typical questions kept per KB.
	KnowledgeBaseProfileMaxQuestions = 5
	// KnowledgeBaseProfileMaxRawTopics caps the raw topic counts stored in stats.
	KnowledgeBaseProfileMaxRawTopics = 30
	// KnowledgeBaseProfileMaxTags caps the tag counts stored in stats.
	KnowledgeBaseProfileMaxTags = 30
	// KnowledgeBaseProfileMaxFolders caps the folder names stored in stats.
	KnowledgeBaseProfileMaxFolders = 20
	// KnowledgeBaseProfileMaxSampleTitles caps titles fed to the model.
	KnowledgeBaseProfileMaxSampleTitles = 60
	// KnowledgeBaseProfileMaxSampleQuestions caps questions fed to the model.
	KnowledgeBaseProfileMaxSampleQuestions = 40
	// KnowledgeBaseProfileMaxSampleDocTypes caps doc type counts.
	KnowledgeBaseProfileMaxSampleDocTypes = 10
	// knowledgeBaseProfileMaxGistRunes bounds the generated gist.
	knowledgeBaseProfileMaxGistRunes = 300
)

// KnowledgeBaseProfileConfig controls automatic generation of the knowledge
// base description. It is opt-in so upgrading a deployment adds no model calls.
type KnowledgeBaseProfileConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// ModelID overrides the chat model; empty falls back to the KB summary model.
	ModelID string `yaml:"model_id,omitempty" json:"model_id,omitempty"`
	// CustomInstructions is appended to the stable system prompt, e.g. the
	// audience the description should address or terms to keep.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty"`
}

// IsEnabled reports whether automatic generation is on for a nil-safe receiver.
func (c *KnowledgeBaseProfileConfig) IsEnabled() bool {
	return c != nil && c.Enabled
}

// Value implements driver.Valuer.
func (c KnowledgeBaseProfileConfig) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan implements sql.Scanner.
func (c *KnowledgeBaseProfileConfig) Scan(value interface{}) error {
	return scanJSONColumn(value, c, "knowledge base profile config")
}

// NamedCount is a (name, count) pair used for tag and topic statistics.
type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// KnowledgeBaseProfileStats is the deterministic part of the profile: every
// number here comes from a database aggregation, never from a model.
type KnowledgeBaseProfileStats struct {
	// DocumentCount is the number of eligible documents at generation time.
	DocumentCount int `json:"document_count"`
	// ProfiledCount is how many of them carried a document profile.
	ProfiledCount int `json:"profiled_count"`
	// FileTypes counts documents per file extension.
	FileTypes []NamedCount `json:"file_types,omitempty"`
	// Tags counts documents per knowledge-base tag.
	Tags []NamedCount `json:"tags,omitempty"`
	// RawTopics counts documents per normalized topic keyword before merging.
	RawTopics []NamedCount `json:"raw_topics,omitempty"`
	// DocTypes counts documents per model-assigned document type.
	DocTypes []NamedCount `json:"doc_types,omitempty"`
	// Folders lists the top-level folder names present.
	Folders []string `json:"folders,omitempty"`
	// EarliestAt and LatestAt bound document creation times.
	EarliestAt *time.Time `json:"earliest_at,omitempty"`
	LatestAt   *time.Time `json:"latest_at,omitempty"`
}

// KnowledgeBaseProfile is the generated knowledge-base description. The gist,
// topics and questions come from one small model call over the aggregate; the
// stats are copied from the aggregate so readers do not have to recompute it.
type KnowledgeBaseProfile struct {
	// Gist is a short statement of what the knowledge base covers.
	Gist string `json:"gist,omitempty"`
	// Topics is the merged topic list (at most KnowledgeBaseProfileMaxTopics).
	Topics []string `json:"topics,omitempty"`
	// TypicalQuestions lists questions this knowledge base can answer.
	TypicalQuestions []string `json:"typical_questions,omitempty"`
	// Stats is the aggregate snapshot the text was generated from.
	Stats KnowledgeBaseProfileStats `json:"stats"`
	// AggregateHash identifies the aggregate the gist was generated from. A
	// mismatch with the live aggregate means the text is stale.
	AggregateHash string `json:"aggregate_hash,omitempty"`
	// Status is one of the KnowledgeBaseProfileStatus* values.
	Status string `json:"status,omitempty"`
	// Error holds the last generation error when Status is failed.
	Error string `json:"error,omitempty"`
	// ModelID records which chat model produced the text.
	ModelID string `json:"model_id,omitempty"`
	// GeneratedAt records when the text was produced.
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
}

// Value implements driver.Valuer.
func (p KnowledgeBaseProfile) Value() (driver.Value, error) { return json.Marshal(p) }

// Scan implements sql.Scanner.
func (p *KnowledgeBaseProfile) Scan(value interface{}) error {
	return scanJSONColumn(value, p, "knowledge base profile")
}

// IsReady reports whether the profile holds generated text for the given
// aggregate hash. An empty hash matches any ready profile.
func (p *KnowledgeBaseProfile) IsReady(aggregateHash string) bool {
	if p == nil || p.Status != KnowledgeBaseProfileStatusReady {
		return false
	}
	return aggregateHash == "" || p.AggregateHash == aggregateHash
}

// HasText reports whether the profile carries anything worth showing.
func (p *KnowledgeBaseProfile) HasText() bool {
	return p != nil && (strings.TrimSpace(p.Gist) != "" || len(p.Topics) > 0 || len(p.TypicalQuestions) > 0)
}

// Normalize bounds the model-produced fields.
func (p *KnowledgeBaseProfile) Normalize() {
	if p == nil {
		return
	}
	p.Gist = truncateRunes(collapseWhitespace(p.Gist), knowledgeBaseProfileMaxGistRunes)
	p.Topics = NormalizeTopicList(p.Topics, KnowledgeBaseProfileMaxTopics)
	p.TypicalQuestions = normalizeQuestionList(p.TypicalQuestions, KnowledgeBaseProfileMaxQuestions)
}

func normalizeQuestionList(questions []string, limit int) []string {
	if len(questions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(questions))
	out := make([]string, 0, len(questions))
	for _, q := range questions {
		q = truncateRunes(collapseWhitespace(q), knowledgeProfileMaxQuestionRunes)
		key := strings.ToLower(q)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, q)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// KnowledgeProfileRow is the lightweight projection of a knowledge row used
// by the knowledge-base aggregation. It deliberately omits content columns.
type KnowledgeProfileRow struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	FileName   string            `json:"file_name"`
	FileType   string            `json:"file_type"`
	FolderPath string            `json:"folder_path"`
	CreatedAt  time.Time         `json:"created_at"`
	Profile    *KnowledgeProfile `json:"profile,omitempty"`
	// Tags is populated by the aggregation from the tag relation table.
	Tags []string `json:"tags,omitempty"`
}

// DisplayTitle returns the title, falling back to the file name.
func (r *KnowledgeProfileRow) DisplayTitle() string {
	if r == nil {
		return ""
	}
	if t := strings.TrimSpace(r.Title); t != "" {
		return t
	}
	return strings.TrimSpace(r.FileName)
}

// KnowledgeBaseProfileAggregate is the deterministic layer between documents
// and the generated description. Everything in it is a count or a sample, so
// deleting a document simply removes its contribution on the next build.
type KnowledgeBaseProfileAggregate struct {
	Stats KnowledgeBaseProfileStats `json:"stats"`
	// SampleTitles is a stratified sample of document titles.
	SampleTitles []string `json:"sample_titles,omitempty"`
	// SampleQuestions is a sample of per-document typical questions, spread
	// across topics so one large topic does not crowd the others out.
	SampleQuestions []string `json:"sample_questions,omitempty"`
	// SampleGists is a small sample of per-document gists.
	SampleGists []string `json:"sample_gists,omitempty"`
	// Hash fingerprints the inputs; see BuildKnowledgeBaseProfileAggregate.
	Hash string `json:"hash"`
}

// IsEmpty reports whether no eligible document contributed.
func (a *KnowledgeBaseProfileAggregate) IsEmpty() bool {
	return a == nil || a.Stats.DocumentCount == 0
}

// topicBucket tracks one normalized topic while aggregating.
type topicBucket struct {
	display   string
	count     int
	questions []string
}

// BuildKnowledgeBaseProfileAggregate folds document rows into counts and
// samples. Rows are expected to be pre-filtered to eligible documents.
func BuildKnowledgeBaseProfileAggregate(rows []*KnowledgeProfileRow) *KnowledgeBaseProfileAggregate {
	agg := &KnowledgeBaseProfileAggregate{}
	if len(rows) == 0 {
		agg.Hash = hashAggregateInputs(nil)
		return agg
	}

	fileTypes := map[string]int{}
	tags := map[string]int{}
	docTypes := map[string]*topicBucket{}
	topics := map[string]*topicBucket{}
	topicOrder := []string{}
	folders := map[string]struct{}{}
	titles := make([]string, 0, len(rows))
	gists := make([]string, 0, len(rows))
	untopicedQuestions := []string{}
	fingerprints := make([]string, 0, len(rows))

	for _, row := range rows {
		if row == nil || row.ID == "" {
			continue
		}
		agg.Stats.DocumentCount++
		if ft := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(row.FileType), ".")); ft != "" {
			fileTypes[ft]++
		}
		for _, tag := range row.Tags {
			if tag = collapseWhitespace(tag); tag != "" {
				tags[tag]++
			}
		}
		if folder := topLevelFolder(row.FolderPath); folder != "" {
			folders[folder] = struct{}{}
		}
		if agg.Stats.EarliestAt == nil || row.CreatedAt.Before(*agg.Stats.EarliestAt) {
			t := row.CreatedAt
			agg.Stats.EarliestAt = &t
		}
		if agg.Stats.LatestAt == nil || row.CreatedAt.After(*agg.Stats.LatestAt) {
			t := row.CreatedAt
			agg.Stats.LatestAt = &t
		}
		if title := row.DisplayTitle(); title != "" {
			titles = append(titles, title)
		}

		fp := row.ID + "|" + row.DisplayTitle() + "|" + strings.Join(row.Tags, ",")
		profile := row.Profile
		if profile != nil && !profile.IsEmpty() {
			agg.Stats.ProfiledCount++
			fp += "|" + profile.Gist + "|" + strings.Join(profile.Topics, ",") +
				"|" + profile.DocType + "|" + profile.TypicalQuestion
			if gist := collapseWhitespace(profile.Gist); gist != "" {
				gists = append(gists, gist)
			}
			if dt := collapseWhitespace(profile.DocType); dt != "" {
				key := TopicKey(dt)
				if b, ok := docTypes[key]; ok {
					b.count++
				} else {
					docTypes[key] = &topicBucket{display: dt, count: 1}
				}
			}
			question := collapseWhitespace(profile.TypicalQuestion)
			assigned := false
			for _, topic := range profile.Topics {
				display := collapseWhitespace(topic)
				key := TopicKey(display)
				if key == "" {
					continue
				}
				b, ok := topics[key]
				if !ok {
					b = &topicBucket{display: display}
					topics[key] = b
					topicOrder = append(topicOrder, key)
				}
				b.count++
				if question != "" && !assigned {
					b.questions = append(b.questions, question)
					assigned = true
				}
			}
			if question != "" && !assigned {
				untopicedQuestions = append(untopicedQuestions, question)
			}
		}
		fingerprints = append(fingerprints, fp)
	}

	agg.Stats.FileTypes = topNamedCounts(fileTypes, 0)
	agg.Stats.Tags = topNamedCounts(tags, KnowledgeBaseProfileMaxTags)
	agg.Stats.Folders = sortedKeys(folders, KnowledgeBaseProfileMaxFolders)

	// Topics ordered by count desc, then by first appearance for stability.
	sort.SliceStable(topicOrder, func(i, j int) bool {
		return topics[topicOrder[i]].count > topics[topicOrder[j]].count
	})
	rawTopics := make([]NamedCount, 0, len(topicOrder))
	for _, key := range topicOrder {
		rawTopics = append(rawTopics, NamedCount{Name: topics[key].display, Count: topics[key].count})
	}
	if len(rawTopics) > KnowledgeBaseProfileMaxRawTopics {
		rawTopics = rawTopics[:KnowledgeBaseProfileMaxRawTopics]
	}
	agg.Stats.RawTopics = rawTopics

	docTypeCounts := make(map[string]int, len(docTypes))
	for _, b := range docTypes {
		docTypeCounts[b.display] = b.count
	}
	agg.Stats.DocTypes = topNamedCounts(docTypeCounts, KnowledgeBaseProfileMaxSampleDocTypes)

	agg.SampleTitles = spreadSample(titles, KnowledgeBaseProfileMaxSampleTitles)
	agg.SampleGists = spreadSample(gists, KnowledgeBaseProfileMaxSampleTitles/3)
	agg.SampleQuestions = roundRobinQuestions(
		topicOrder, topics, untopicedQuestions, KnowledgeBaseProfileMaxSampleQuestions)
	agg.Hash = hashAggregateInputs(fingerprints)
	return agg
}

// hashAggregateInputs fingerprints the per-document contributions in a
// stable order so the same document set yields the same hash regardless of
// query ordering.
func hashAggregateInputs(fingerprints []string) string {
	sorted := append([]string(nil), fingerprints...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, fp := range sorted {
		h.Write([]byte(fp))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func topLevelFolder(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return path
}

func topNamedCounts(counts map[string]int, limit int) []NamedCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]NamedCount, 0, len(counts))
	for name, count := range counts {
		out = append(out, NamedCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortedKeys(set map[string]struct{}, limit int) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// spreadSample picks up to limit items evenly spaced across the input so a
// sample of titles covers the whole corpus rather than the newest uploads.
func spreadSample(items []string, limit int) []string {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]string(nil), items...)
	}
	out := make([]string, 0, limit)
	step := float64(len(items)) / float64(limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[int(float64(i)*step)])
	}
	return out
}

// roundRobinQuestions takes one question per topic in count order, then a
// second per topic, and so on, so the sample is spread across topics.
func roundRobinQuestions(order []string, topics map[string]*topicBucket, extra []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, limit)
	add := func(q string) bool {
		key := strings.ToLower(q)
		if _, ok := seen[key]; ok {
			return len(out) < limit
		}
		seen[key] = struct{}{}
		out = append(out, q)
		return len(out) < limit
	}
	for round := 0; ; round++ {
		progressed := false
		for _, key := range order {
			b := topics[key]
			if round < len(b.questions) {
				progressed = true
				if !add(b.questions[round]) {
					return out
				}
			}
		}
		if !progressed {
			break
		}
	}
	for _, q := range extra {
		if !add(q) {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scanJSONColumn(value interface{}, target interface{}, label string) error {
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
		return errors.New("unsupported " + label + " column type")
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, target)
}
