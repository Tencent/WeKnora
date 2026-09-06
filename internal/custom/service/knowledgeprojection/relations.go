package knowledgeprojection

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"gopkg.in/yaml.v3"
)

const relationConfidenceThreshold = 0.70

var semanticRelationTypes = map[string]struct{}{
	"contradicts": {}, "complements": {}, "explains": {}, "example_of": {}, "part_of": {},
}

var legacyRelationTypes = map[string]struct{}{
	"derived_from": {}, "supports": {}, "related_to": {},
}

// RelationInput is the P4 representation of a pending P3 relation. P3 only
// carries candidate IDs; P4 adds the resolved page scope and evidence time.
type RelationInput struct {
	RelationID           string   `json:"relation_id"`
	SourceCandidateID    string   `json:"source_candidate_id"`
	TargetCandidateID    string   `json:"target_candidate_id"`
	RelationType         string   `json:"relation_type"`
	EvidenceIDs          []string `json:"evidence_ids"`
	Confidence           float64  `json:"confidence"`
	TimeRange            string   `json:"time_range"`
	SourceVideoID        string   `json:"source_video_id"`
	TranscriptGeneration string   `json:"transcript_generation"`
}

// RelationAuditRecord is retained for every accepted, rejected, or deferred
// relation. Rejected records never reach the Wiki writer.
type RelationAuditRecord struct {
	RelationInput
	SourceWikiPageID string `json:"source_wiki_page_id,omitempty"`
	TargetWikiPageID string `json:"target_wiki_page_id,omitempty"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
}

type SemanticRelation struct {
	RelationID       string   `json:"relation_id"`
	SourceObjectID   string   `json:"source_object_id"`
	SourceWikiPageID string   `json:"source_wiki_page_id"`
	TargetObjectID   string   `json:"target_object_id"`
	TargetWikiPageID string   `json:"target_wiki_page_id"`
	RelationType     string   `json:"relation_type"`
	EvidenceIDs      []string `json:"evidence_ids"`
	Confidence       float64  `json:"confidence"`
	TimeRange        string   `json:"time_range"`
}

// ReadingAssociation describes a Wiki double-link independently from a
// semantic relation. It intentionally stores no inferred relation type.
type ReadingAssociation struct {
	SourceWikiPageID string `json:"source_wiki_page_id"`
	TargetWikiPageID string `json:"target_wiki_page_id,omitempty"`
	TargetSlug       string `json:"target_slug,omitempty"`
	TargetTitle      string `json:"target_title,omitempty"`
	Valid            bool   `json:"valid"`
}

type RelationProjection struct {
	Audits       []RelationAuditRecord `json:"audits"`
	Semantic     []SemanticRelation    `json:"semantic"`
	Reading      []ReadingAssociation  `json:"reading"`
	InvalidLinks []ReadingAssociation  `json:"invalid_links"`
	Orphans      []string              `json:"orphans"`
}

var wikiDoubleLinkPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)

// ProjectReadingAssociations extracts existing Wiki double-links without
// treating them as semantic edges. A link is valid only when it resolves to a
// real page by ID, slug, or exact title; unresolved links remain auditable.
func ProjectReadingAssociations(pages []weknora.WikiPage) (valid []ReadingAssociation, invalid []ReadingAssociation) {
	byID, bySlug, byTitle := map[string]weknora.WikiPage{}, map[string]weknora.WikiPage{}, map[string]weknora.WikiPage{}
	for _, page := range pages {
		if page.ID == "" {
			continue
		}
		byID[page.ID] = page
		bySlug[strings.TrimSpace(page.Slug)] = page
		byTitle[strings.TrimSpace(page.Title)] = page
	}
	seen := map[string]struct{}{}
	for _, source := range pages {
		matches := wikiDoubleLinkPattern.FindAllStringSubmatch(source.Content, -1)
		for _, match := range matches {
			targetKey := strings.TrimSpace(match[1])
			title := targetKey
			if len(match) > 2 && strings.TrimSpace(match[2]) != "" {
				title = strings.TrimSpace(match[2])
			}
			target, ok := byID[targetKey]
			if !ok {
				target, ok = bySlug[targetKey]
			}
			if !ok {
				target, ok = byTitle[title]
			}
			association := ReadingAssociation{SourceWikiPageID: source.ID, TargetTitle: title}
			if ok {
				association.TargetWikiPageID, association.TargetSlug, association.TargetTitle, association.Valid = target.ID, target.Slug, target.Title, true
			}
			key := source.ID + "\x00" + targetKey
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			if association.Valid {
				valid = append(valid, association)
			} else {
				invalid = append(invalid, association)
			}
		}
		for _, targetID := range source.OutLinks {
			if strings.TrimSpace(targetID) == "" {
				continue
			}
			if target, ok := byID[strings.TrimSpace(targetID)]; ok {
				key := source.ID + "\x00" + target.ID
				if _, duplicate := seen[key]; !duplicate {
					seen[key] = struct{}{}
					valid = append(valid, ReadingAssociation{SourceWikiPageID: source.ID, TargetWikiPageID: target.ID, TargetSlug: target.Slug, TargetTitle: target.Title, Valid: true})
				}
			}
		}
	}
	sort.SliceStable(valid, func(i, j int) bool {
		return valid[i].SourceWikiPageID+valid[i].TargetWikiPageID < valid[j].SourceWikiPageID+valid[j].TargetWikiPageID
	})
	sort.SliceStable(invalid, func(i, j int) bool {
		return invalid[i].SourceWikiPageID+invalid[i].TargetTitle < invalid[j].SourceWikiPageID+invalid[j].TargetTitle
	})
	return valid, invalid
}

// AuditRelations resolves candidate IDs against the real P4-03 page map and
// applies the frozen P4-00 relationship contract. It performs no writes.
func AuditRelations(inputs []RelationInput, pages []ObjectPageResult, expectedVideoID, expectedGeneration string, durationMs int64) RelationProjection {
	result := RelationProjection{Audits: make([]RelationAuditRecord, 0, len(inputs))}
	pageByCandidate := make(map[string]ObjectPageResult, len(pages))
	for _, page := range pages {
		if strings.TrimSpace(page.CandidateID) != "" {
			pageByCandidate[page.CandidateID] = page
		}
	}
	seenEdges := map[string]struct{}{}
	acceptedSource := map[string]bool{}
	for _, input := range inputs {
		audit := RelationAuditRecord{RelationInput: normalizeRelationInput(input)}
		source, sourceOK := pageByCandidate[audit.SourceCandidateID]
		target, targetOK := pageByCandidate[audit.TargetCandidateID]
		if sourceOK {
			audit.SourceWikiPageID = source.WikiPageID
		}
		if targetOK {
			audit.TargetWikiPageID = target.WikiPageID
		}
		status, reason := validateRelation(audit.RelationInput, source, sourceOK, target, targetOK, expectedVideoID, expectedGeneration, durationMs)
		if status == "accepted" {
			key := source.WikiPageID + "\x00" + audit.RelationType + "\x00" + target.WikiPageID
			if _, duplicate := seenEdges[key]; duplicate {
				status, reason = "duplicate", "relation duplicates an accepted source/type/target edge"
			} else {
				seenEdges[key] = struct{}{}
				acceptedSource[source.CandidateID] = true
				audit.RelationID = firstNonEmptyRelationID(audit.RelationID, source.WikiPageID, audit.RelationType, target.WikiPageID)
				result.Semantic = append(result.Semantic, SemanticRelation{
					RelationID: audit.RelationID, SourceObjectID: source.CandidateID, SourceWikiPageID: source.WikiPageID,
					TargetObjectID: target.CandidateID, TargetWikiPageID: target.WikiPageID, RelationType: audit.RelationType,
					EvidenceIDs: append([]string(nil), audit.EvidenceIDs...), Confidence: audit.Confidence, TimeRange: audit.TimeRange,
				})
			}
		}
		audit.Status, audit.Reason = status, reason
		result.Audits = append(result.Audits, audit)
	}
	for _, page := range pages {
		if !acceptedSource[page.CandidateID] {
			result.Orphans = append(result.Orphans, page.CandidateID)
		}
	}
	sort.SliceStable(result.Semantic, func(i, j int) bool { return result.Semantic[i].RelationID < result.Semantic[j].RelationID })
	sort.SliceStable(result.Audits, func(i, j int) bool { return result.Audits[i].RelationID < result.Audits[j].RelationID })
	sort.Strings(result.Orphans)
	return result
}

func validateRelation(input RelationInput, source ObjectPageResult, sourceOK bool, target ObjectPageResult, targetOK bool, videoID, generation string, durationMs int64) (string, string) {
	if strings.TrimSpace(input.RelationID) == "" || strings.TrimSpace(input.SourceCandidateID) == "" || strings.TrimSpace(input.TargetCandidateID) == "" {
		return "rejected", "relation identity is incomplete"
	}
	if input.SourceCandidateID == input.TargetCandidateID {
		return "rejected", "self relation is not allowed"
	}
	relationType := strings.ToLower(strings.TrimSpace(input.RelationType))
	if _, ok := legacyRelationTypes[relationType]; ok {
		return "rejected_legacy_relation_type", "legacy relation type is not in the current semantic contract"
	}
	if _, ok := semanticRelationTypes[relationType]; !ok {
		return "rejected", "relation type is not allowed"
	}
	if !sourceOK || !targetOK {
		return "deferred", "source or target Wiki page is missing"
	}
	if source.WikiPageID == "" || target.WikiPageID == "" || source.WikiPageID == target.WikiPageID {
		return "rejected", "source and target must resolve to distinct real Wiki page IDs"
	}
	if strings.TrimSpace(source.SourceVideoID) != strings.TrimSpace(videoID) || strings.TrimSpace(target.SourceVideoID) != strings.TrimSpace(videoID) ||
		strings.TrimSpace(source.TranscriptGeneration) != strings.TrimSpace(generation) || strings.TrimSpace(target.TranscriptGeneration) != strings.TrimSpace(generation) {
		return "rejected", "source or target Wiki page does not belong to the active video generation"
	}
	if source.PrimaryType == "" || target.PrimaryType == "" || !relationAllowedForTypes(relationType, source.PrimaryType, target.PrimaryType) {
		return "rejected", "source/target type combination is not allowed by the five-type matrix"
	}
	if strings.TrimSpace(input.SourceVideoID) != strings.TrimSpace(videoID) || strings.TrimSpace(input.TranscriptGeneration) != strings.TrimSpace(generation) {
		return "rejected", "relation does not belong to the active video generation"
	}
	if len(compactRelationValues(input.EvidenceIDs)) == 0 {
		return "rejected", "relation evidence_ids must not be empty"
	}
	if input.Confidence < relationConfidenceThreshold {
		return "rejected", "relation confidence is below 0.70"
	}
	if strings.TrimSpace(input.TimeRange) == "" {
		return "rejected", "relation time_range must not be empty"
	}
	if err := knowledge.ValidateFirstStageTimeRange(input.TimeRange, durationMs); err != nil {
		return "rejected", "relation time_range is invalid: " + err.Error()
	}
	return "accepted", "relation passed type, target, evidence, time and confidence checks"
}

func relationAllowedForTypes(relationType string, source, target knowledge.KnowledgeType) bool {
	allowed := map[knowledge.KnowledgeType]map[knowledge.KnowledgeType]map[string]struct{}{
		knowledge.TypeConcept: {
			knowledge.TypeConcept:     {"complements": {}, "contradicts": {}, "part_of": {}},
			knowledge.TypeMethodology: {"explains": {}, "part_of": {}}, knowledge.TypeCase: {"example_of": {}}, knowledge.TypeInsight: {"explains": {}},
		},
		knowledge.TypeMethodology: {knowledge.TypeConcept: {"explains": {}}, knowledge.TypeMethodology: {"complements": {}, "part_of": {}}},
		knowledge.TypeCase:        {knowledge.TypeConcept: {"example_of": {}}, knowledge.TypeMethodology: {"example_of": {}}},
		knowledge.TypeInsight:     {knowledge.TypeConcept: {"explains": {}, "contradicts": {}}, knowledge.TypeInsight: {"complements": {}, "contradicts": {}}},
	}
	types, ok := allowed[source]
	if !ok {
		return false
	}
	_, ok = types[target][relationType]
	return ok
}

func normalizeRelationInput(input RelationInput) RelationInput {
	input.RelationID = strings.TrimSpace(input.RelationID)
	input.SourceCandidateID = strings.TrimSpace(input.SourceCandidateID)
	input.TargetCandidateID = strings.TrimSpace(input.TargetCandidateID)
	input.RelationType = strings.ToLower(strings.TrimSpace(input.RelationType))
	input.SourceVideoID = strings.TrimSpace(input.SourceVideoID)
	input.TranscriptGeneration = strings.TrimSpace(input.TranscriptGeneration)
	input.TimeRange = strings.TrimSpace(input.TimeRange)
	input.EvidenceIDs = compactRelationValues(input.EvidenceIDs)
	return input
}

func compactRelationValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				out = append(out, value)
			}
		}
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyRelationID(id, sourcePage, relationType, targetPage string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return sourcePage + ":" + relationType + ":" + targetPage
}

// RelationPagePublisher applies accepted semantic relations after all targets
// have passed audit. Existing pages are updated idempotently by version.
type RelationPagePublisher struct {
	Wiki              Wiki
	KBID              string
	WriteReadingLinks bool
}

type RelationWriteResult struct {
	WikiPageID string `json:"wiki_page_id"`
	Slug       string `json:"slug"`
	Action     string `json:"action"`
	Relations  int    `json:"relations"`
}

// PublishRelations performs a read-before-write preflight, then updates only
// source pages. A missing target or an audit rejection therefore cannot create
// a dangling relation.
func (p RelationPagePublisher) PublishRelations(ctx context.Context, pages []ObjectPageResult, projection RelationProjection) ([]RelationWriteResult, error) {
	if p.Wiki == nil || strings.TrimSpace(p.KBID) == "" {
		return nil, fmt.Errorf("relation publisher requires Wiki client and knowledge base")
	}
	acceptedAudits := map[string]RelationAuditRecord{}
	for _, audit := range projection.Audits {
		if audit.Status == "accepted" {
			acceptedAudits[audit.RelationID+"\x00"+audit.SourceWikiPageID+"\x00"+audit.TargetWikiPageID] = audit
		}
	}
	for _, edge := range projection.Semantic {
		key := edge.RelationID + "\x00" + edge.SourceWikiPageID + "\x00" + edge.TargetWikiPageID
		if _, ok := acceptedAudits[key]; !ok {
			return nil, fmt.Errorf("semantic relation %s has no matching accepted audit", edge.RelationID)
		}
	}
	pageByID := map[string]ObjectPageResult{}
	for _, page := range pages {
		pageByID[page.WikiPageID] = page
	}
	currentByID := map[string]*weknora.WikiPage{}
	for _, edge := range projection.Semantic {
		for _, pageID := range []string{edge.SourceWikiPageID, edge.TargetWikiPageID} {
			if _, loaded := currentByID[pageID]; loaded {
				continue
			}
			pageResult, ok := pageByID[pageID]
			if !ok || pageResult.Slug == "" {
				return nil, fmt.Errorf("relation page %s is not in the P4-03 page map", pageID)
			}
			page, err := p.Wiki.GetPage(ctx, p.KBID, pageResult.Slug)
			if err != nil || page == nil || page.ID != pageID {
				if err != nil {
					return nil, fmt.Errorf("read relation page %s: %w", pageID, err)
				}
				return nil, fmt.Errorf("read relation page %s: page identity is not readable", pageID)
			}
			currentByID[pageID] = page
		}
	}
	bySource := map[string][]SemanticRelation{}
	for _, edge := range projection.Semantic {
		bySource[edge.SourceWikiPageID] = append(bySource[edge.SourceWikiPageID], edge)
	}
	sourceIDs := make([]string, 0, len(bySource))
	for sourceID := range bySource {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Strings(sourceIDs)
	results := make([]RelationWriteResult, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		page := currentByID[sourceID]
		content, changed, count, err := mergeSemanticRelations(page.Content, bySource[sourceID], p.WriteReadingLinks)
		if err != nil {
			return results, fmt.Errorf("prepare relation page %s: %w", sourceID, err)
		}
		action := "unchanged"
		written := page
		if changed {
			written, err = p.Wiki.UpsertPage(ctx, p.KBID, weknora.WikiPageWrite{Slug: page.Slug, Title: page.Title, PageType: page.PageType, Status: page.Status, Content: content, Summary: page.Summary, SourceRefs: page.SourceRefs, ChunkRefs: page.ChunkRefs, Version: page.Version})
			if err != nil {
				return results, fmt.Errorf("write relation page %s: %w", sourceID, err)
			}
			action = "updated"
		}
		if written == nil || written.ID != sourceID {
			return results, fmt.Errorf("relation page %s write returned an unexpected page identity", sourceID)
		}
		readBack, err := p.Wiki.GetPage(ctx, p.KBID, page.Slug)
		if err != nil || readBack == nil || !containsSemanticRelations(readBack.Content, bySource[sourceID]) {
			if err != nil {
				return results, fmt.Errorf("read back relation page %s: %w", sourceID, err)
			}
			return results, fmt.Errorf("read back relation page %s does not contain the audited relations", sourceID)
		}
		results = append(results, RelationWriteResult{WikiPageID: sourceID, Slug: page.Slug, Action: action, Relations: count})
	}
	return results, nil
}

func mergeSemanticRelations(content string, edges []SemanticRelation, writeReadingLinks bool) (string, bool, int, error) {
	fm, body, err := parsePageFrontmatter(content)
	if err != nil {
		return "", false, 0, err
	}
	relations := make([]map[string]any, 0, len(edges))
	for _, edge := range edges {
		relations = append(relations, map[string]any{"relation_id": edge.RelationID, "relation_type": edge.RelationType, "target_object_id": edge.TargetObjectID, "target_wiki_page_id": edge.TargetWikiPageID, "evidence_ids": edge.EvidenceIDs, "confidence": edge.Confidence, "time_range": edge.TimeRange})
	}
	fm["relations"] = relations
	if writeReadingLinks {
		// Reading links are an explicit opt-in and never replace semantic edges.
		links := make([]string, 0, len(edges))
		for _, edge := range edges {
			links = append(links, "[["+edge.TargetWikiPageID+"]]")
		}
		fm["related_content"] = links
	}
	encoded, err := yaml.Marshal(fm)
	if err != nil {
		return "", false, 0, fmt.Errorf("encode relation frontmatter: %w", err)
	}
	updated := "---\n" + string(encoded) + "---" + body
	return updated, updated != content, len(relations), nil
}

func parsePageFrontmatter(content string) (map[string]any, string, error) {
	trimmed := strings.TrimSpace(content)
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("Wiki page is missing YAML frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("Wiki page frontmatter is unterminated")
	}
	fm := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return nil, "", fmt.Errorf("decode frontmatter: %w", err)
	}
	body := strings.Join(lines[end+1:], "\n")
	if body != "" {
		body = "\n" + body
	}
	return fm, body, nil
}

func containsSemanticRelations(content string, edges []SemanticRelation) bool {
	fm, _, err := parsePageFrontmatter(content)
	if err != nil {
		return false
	}
	items, ok := fm["relations"].([]any)
	if !ok {
		return false
	}
	seen := map[string]struct{}{}
	for _, item := range items {
		if data, ok := item.(map[string]any); ok {
			seen[fmt.Sprint(data["relation_id"])] = struct{}{}
		}
	}
	for _, edge := range edges {
		if _, ok := seen[edge.RelationID]; !ok {
			return false
		}
	}
	return true
}
