package knowledge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SplitStatus describes how a classified candidate was handled before the
// type-specific publish gates run.
type SplitStatus string

const (
	SplitStatusUnchanged SplitStatus = "unchanged"
	SplitStatusSplit     SplitStatus = "split"
	SplitStatusRejected  SplitStatus = "rejected"
)

// PendingRelationDescription is an in-memory/P3 artifact only. It intentionally
// points at split candidate IDs instead of Wiki page IDs; P4 decides whether a
// relation can be written after the target pages exist and pass their gates.
type PendingRelationDescription struct {
	RelationID        string   `json:"relation_id"`
	SourceCandidateID string   `json:"source_candidate_id"`
	TargetCandidateID string   `json:"target_candidate_id"`
	RelationType      string   `json:"relation_type"`
	Description       string   `json:"description"`
	EvidenceIDs       []string `json:"evidence_ids"`
}

// RejectedProposition records content that must not become an independent
// Wiki object. Keeping its evidence here prevents a filtering decision from
// silently dropping the source trace.
type RejectedProposition struct {
	CandidateID string        `json:"candidate_id"`
	Title       string        `json:"title"`
	PrimaryType KnowledgeType `json:"primary_type"`
	Reason      string        `json:"reason"`
	EvidenceIDs []string      `json:"evidence_ids"`
}

// SplitResult is the complete deterministic output of P3-03. Objects are
// ready for P3-04 validation; Relations are descriptions for P4 only.
type SplitResult struct {
	CandidateID           string                       `json:"candidate_id"`
	OriginalTitle         string                       `json:"original_title"`
	OriginalPrimaryType   KnowledgeType                `json:"original_primary_type"`
	OriginalEntitySubType string                       `json:"original_entity_sub_type,omitempty"`
	SourceDocumentID      string                       `json:"source_document_id"`
	SourceVideoID         string                       `json:"source_video_id"`
	TranscriptGeneration  string                       `json:"transcript_generation"`
	OriginalEvidenceIDs   []string                     `json:"original_evidence_ids"`
	Status                SplitStatus                  `json:"status"`
	Reason                string                       `json:"reason,omitempty"`
	Objects               []ClassifiedKnowledge        `json:"objects"`
	Relations             []PendingRelationDescription `json:"relations"`
	Rejected              []RejectedProposition        `json:"rejected"`
}

type schemaGroup struct {
	primaryType   KnowledgeType
	entitySubType string
	keys          []string
	count         int
}

// SplitClassifiedKnowledge detects independently retrievable propositions in
// one classified candidate. It is deliberately metadata-only: no source text,
// Wiki API, or Graph client is reachable from this function.
func SplitClassifiedKnowledge(candidate ClassifiedKnowledge) (SplitResult, error) {
	if err := candidate.Validate(); err != nil {
		return SplitResult{}, fmt.Errorf("validate classified knowledge for split: %w", err)
	}

	result := SplitResult{
		CandidateID:           candidate.CandidateID,
		OriginalTitle:         strings.TrimSpace(candidate.Title),
		OriginalPrimaryType:   candidate.PrimaryType,
		OriginalEntitySubType: strings.TrimSpace(candidate.EntitySubType),
		SourceDocumentID:      candidate.SourceDocumentID,
		SourceVideoID:         candidate.SourceVideoID,
		TranscriptGeneration:  candidate.TranscriptGeneration,
		OriginalEvidenceIDs:   sortedEvidenceIDs(candidate.EvidenceIDs),
		Objects:               []ClassifiedKnowledge{},
		Relations:             []PendingRelationDescription{},
		Rejected:              []RejectedProposition{},
	}

	if isOrdinaryWordMeaning(candidate) {
		result.Status = SplitStatusRejected
		result.Reason = "ordinary_word_meaning"
		result.Rejected = []RejectedProposition{{
			CandidateID: candidate.CandidateID,
			Title:       strings.TrimSpace(candidate.Title),
			PrimaryType: candidate.PrimaryType,
			Reason:      result.Reason,
			EvidenceIDs: sortedEvidenceIDs(candidate.EvidenceIDs),
		}}
		if err := result.Validate(); err != nil {
			return SplitResult{}, err
		}
		return result, nil
	}

	groups := splitSchemaGroups(candidate)
	titleParts := titlePartsForCandidate(candidate)
	shouldSplit := len(titleParts) > 1 || shouldSplitBySchema(candidate, groups)
	if !shouldSplit {
		result.Status = SplitStatusUnchanged
		result.Objects = []ClassifiedKnowledge{cloneClassifiedKnowledge(candidate)}
		if err := result.Validate(); err != nil {
			return SplitResult{}, err
		}
		return result, nil
	}

	objects := buildSplitObjects(candidate, groups, titleParts)
	if len(objects) < 2 {
		// A detector must never turn a candidate into an incomplete object. Keep
		// the original until a later stage has enough evidence to split it.
		result.Status = SplitStatusUnchanged
		result.Reason = "split_signal_not_actionable"
		result.Objects = []ClassifiedKnowledge{cloneClassifiedKnowledge(candidate)}
		if err := result.Validate(); err != nil {
			return SplitResult{}, err
		}
		return result, nil
	}

	result.Status = SplitStatusSplit
	result.Reason = splitReason(groups, titleParts)
	result.Objects = objects
	result.Relations = buildPendingRelations(candidate, objects)
	if err := result.Validate(); err != nil {
		return SplitResult{}, err
	}
	return result, nil
}

// SplitCandidate classifies a P3-01 candidate and immediately applies the
// P3-03 split/filter pass. It is the orchestration-friendly entry point when
// the caller has not yet materialized a ClassifiedKnowledge value.
func SplitCandidate(candidate Candidate, context DocumentContext) (SplitResult, error) {
	classified, err := Classify(candidate, context)
	if err != nil {
		return SplitResult{}, err
	}
	return SplitClassifiedKnowledge(classified)
}

// Split is the canonical short entry point for the P3-03 pipeline stage.
func Split(candidate ClassifiedKnowledge) (SplitResult, error) {
	return SplitClassifiedKnowledge(candidate)
}

// DetectAndSplit is a descriptive alias used by orchestration callers.
func DetectAndSplit(candidate ClassifiedKnowledge) (SplitResult, error) {
	return SplitClassifiedKnowledge(candidate)
}

// SplitPropositions is kept as a short entry point for pipeline code.
func SplitPropositions(candidate ClassifiedKnowledge) (SplitResult, error) {
	return SplitClassifiedKnowledge(candidate)
}

// DecodeSplitResult decodes the persisted P3-03 artifact with the same
// unknown-field protection used by the P3-01/P3-02 contracts.
func DecodeSplitResult(data []byte) (SplitResult, error) {
	var value SplitResult
	if err := decodeStrict(data, &value); err != nil {
		return SplitResult{}, fmt.Errorf("decode split result: %w", err)
	}
	SortSplitResult(&value)
	if err := value.Validate(); err != nil {
		return SplitResult{}, err
	}
	return value, nil
}

// HasMultiplePropositions is a convenience predicate for metrics and routing.
// Invalid candidates are treated as not actionable; callers that need the
// reason should use SplitClassifiedKnowledge directly.
func HasMultiplePropositions(candidate ClassifiedKnowledge) bool {
	result, err := SplitClassifiedKnowledge(candidate)
	return err == nil && result.Status == SplitStatusSplit
}

func cloneClassifiedKnowledge(value ClassifiedKnowledge) ClassifiedKnowledge {
	value.Title = strings.TrimSpace(value.Title)
	value.CoreContent = strings.TrimSpace(value.CoreContent)
	value.EntitySubType = strings.TrimSpace(value.EntitySubType)
	value.StructureFields = cloneStructureFields(value.StructureFields)
	value.EvidenceIDs = sortedEvidenceIDs(value.EvidenceIDs)
	return value
}

func splitSchemaGroups(candidate ClassifiedKnowledge) []schemaGroup {
	if candidate.PrimaryType == TypeEntity {
		return []schemaGroup{{
			primaryType:   TypeEntity,
			entitySubType: candidate.EntitySubType,
			keys:          frameworkKeys(TypeEntity, candidate.EntitySubType),
			count:         countPopulatedFields(candidate.StructureFields, frameworkKeys(TypeEntity, candidate.EntitySubType)),
		}}
	}

	rules := frameworkClassificationRules()
	groups := make([]schemaGroup, 0, len(rules))
	seenTypes := make(map[KnowledgeType]bool)
	for _, rule := range rules {
		if rule.primaryType == TypeEntity || seenTypes[rule.primaryType] {
			continue
		}
		count := countPopulatedFields(candidate.StructureFields, rule.fields)
		if count == 0 {
			continue
		}
		seenTypes[rule.primaryType] = true
		groups = append(groups, schemaGroup{primaryType: rule.primaryType, keys: append([]string(nil), rule.fields...), count: count})
	}
	return groups
}

func shouldSplitBySchema(candidate ClassifiedKnowledge, groups []schemaGroup) bool {
	if candidate.PrimaryType == TypeEntity || len(groups) < 2 {
		return false
	}
	// A stray field should not split a valid object. A second type needs at
	// least two populated fields unless the title/claim detector already found
	// an explicit proposition boundary.
	for _, group := range groups {
		if group.primaryType != candidate.PrimaryType && group.count >= 2 {
			return true
		}
	}
	return false
}

func buildSplitObjects(candidate ClassifiedKnowledge, groups []schemaGroup, titleParts []string) []ClassifiedKnowledge {
	if len(groups) == 0 {
		return nil
	}
	if len(titleParts) > 1 && len(groups) == 1 {
		objects := make([]ClassifiedKnowledge, 0, len(titleParts))
		for index, part := range titleParts {
			fields := fieldsForSplitPart(candidate.StructureFields, groups[0], part)
			objects = append(objects, newSplitObject(candidate, groups[0], fields, part, index))
		}
		return objects
	}

	if len(titleParts) == len(groups) {
		assigned := make([]bool, len(groups))
		objects := make([]ClassifiedKnowledge, 0, len(groups))
		for index, part := range titleParts {
			groupIndex := matchingGroupIndex(part, groups, assigned)
			if groupIndex < 0 {
				groupIndex = firstUnassignedGroup(assigned)
			}
			if groupIndex < 0 {
				return nil
			}
			assigned[groupIndex] = true
			group := groups[groupIndex]
			objects = append(objects, newSplitObject(candidate, group, fieldsForSplitPart(candidate.StructureFields, group, part), part, index))
		}
		return objects
	}

	objects := make([]ClassifiedKnowledge, 0, len(groups))
	for index, group := range groups {
		title := ""
		if index < len(titleParts) {
			title = titleParts[index]
		}
		if strings.TrimSpace(title) == "" {
			title = deriveGroupTitle(candidate, group)
		}
		objects = append(objects, newSplitObject(candidate, group, fieldsForSplitPart(candidate.StructureFields, group, title), title, index))
	}
	return objects
}

func newSplitObject(parent ClassifiedKnowledge, group schemaGroup, fields map[string]string, title string, index int) ClassifiedKnowledge {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(parent.Title)
	}
	child := cloneClassifiedKnowledge(parent)
	child.CandidateID = parent.CandidateID + "#split-" + strconv.Itoa(index+1)
	child.PrimaryType = group.primaryType
	child.EntitySubType = group.entitySubType
	child.Title = title
	child.StructureFields = fields
	child.CoreContent = splitCoreContent(fields, group.primaryType)
	if child.CoreContent == "" {
		child.CoreContent = title
	}
	return child
}

func fieldsForSplitPart(fields map[string]string, group schemaGroup, part string) map[string]string {
	selected := make(map[string]string)
	for _, key := range group.keys {
		if value := strings.TrimSpace(fields[key]); value != "" {
			selected[key] = narrowFieldValue(value, part)
		}
	}
	if len(selected) == 0 {
		for _, key := range group.keys {
			if value := strings.TrimSpace(fields[key]); value != "" {
				selected[key] = value
			}
		}
	}
	return selected
}

func splitCoreContent(fields map[string]string, primaryType KnowledgeType) string {
	parts := make([]string, 0, 2)
	for _, key := range frameworkTitleKeys(primaryType, "") {
		if value := strings.TrimSpace(fields[key]); value != "" {
			parts = append(parts, firstClause(value))
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, "；")
}

func narrowFieldValue(value, part string) string {
	clauses := splitClauses(value)
	if len(clauses) < 2 || strings.TrimSpace(part) == "" {
		return value
	}
	normalizedPart := NormalizeIdentity(part)
	if normalizedPart == "" {
		return value
	}
	matched := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		normalizedClause := NormalizeIdentity(clause)
		if strings.Contains(normalizedClause, normalizedPart) || strings.Contains(normalizedPart, normalizedClause) {
			matched = append(matched, clause)
		}
	}
	if len(matched) == 0 {
		return value
	}
	return strings.Join(matched, "；")
}

func deriveGroupTitle(candidate ClassifiedKnowledge, group schemaGroup) string {
	keys := frameworkTitleKeys(group.primaryType, group.entitySubType)
	for _, key := range keys {
		if value := strings.TrimSpace(candidate.StructureFields[key]); value != "" {
			return firstClause(value)
		}
	}
	return strings.TrimSpace(candidate.Title) + " " + knowledgeTypeLabel(group.primaryType)
}

func frameworkTitleKeys(primaryType KnowledgeType, entitySubType string) []string {
	return frameworkKeys(primaryType, entitySubType)
}

func knowledgeTypeLabel(value KnowledgeType) string {
	if label := frameworkLabel(value, ""); label != "" {
		return label
	}
	return string(value)
}

func matchingGroupIndex(part string, groups []schemaGroup, assigned []bool) int {
	for index, group := range groups {
		if assigned[index] || !titleSuggestsType(part, group.primaryType) {
			continue
		}
		return index
	}
	return -1
}

func firstUnassignedGroup(assigned []bool) int {
	for index, used := range assigned {
		if !used {
			return index
		}
	}
	return -1
}

func titleSuggestsType(value string, primaryType KnowledgeType) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch primaryType {
	case TypeCase:
		return containsAny(lower, "案例", "投资", "项目", "事故", "事件", "上线", "发布")
	case TypeInsight:
		return containsAny(lower, "判断", "低估", "高估", "应该", "应当", "会", "将", "消亡", "稀缺", "被")
	case TypeConcept:
		return containsAny(lower, "概念", "理论", "效应", "范式", "原理")
	case TypeMethodology:
		return containsAny(lower, "方法", "框架", "策略", "归因", "流程", "判断")
	default:
		return false
	}
}

func titlePartsForCandidate(candidate ClassifiedKnowledge) []string {
	if parts, ok := splitTitleByConnector(candidate.Title, candidate.PrimaryType); ok {
		return parts
	}
	if candidate.PrimaryType == TypeConcept {
		if subjects := conceptDefinitionSubjects(candidate.StructureFields["definition"]); len(subjects) > 1 {
			return subjects
		}
	}
	if candidate.PrimaryType == TypeInsight {
		claim := candidate.StructureFields["claim"]
		if parts, separator := splitClaimPartsWithSeparator(claim); len(parts) > 1 && !isUnifiedInsight(claim, separator, parts) {
			return parts
		}
	}
	return nil
}

func splitTitleByConnector(title string, primaryType KnowledgeType) ([]string, bool) {
	base := strings.TrimSpace(title)
	for _, suffix := range []string{"的范式之争", "的比较", "之争"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSpace(strings.TrimSuffix(base, suffix))
			break
		}
	}
	base = strings.ReplaceAll(base, " VS ", "|")
	base = strings.ReplaceAll(base, " vs ", "|")
	base = strings.ReplaceAll(base, " Vs ", "|")
	base = strings.ReplaceAll(base, "&", "|")
	base = strings.ReplaceAll(base, "／", "|")
	base = strings.ReplaceAll(base, "/", "|")

	separator := firstTitleConnector(base)
	if separator == "" || likelyWordInternalConnector(base, separator) {
		return nil, false
	}
	parts := splitAllTitleConnectors(base)
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "，,；;:：()（）"))
		if len([]rune(part)) < 2 {
			return nil, false
		}
		cleaned = append(cleaned, part)
	}
	if len(cleaned) < 2 || !shouldSplitTitle(primaryType, title, separator, cleaned) {
		return nil, false
	}
	return cleaned, true
}

func firstTitleConnector(value string) string {
	for _, candidateSeparator := range []string{"并且", "以及", "且", "与", "和", "及", "|"} {
		if strings.Contains(value, candidateSeparator) {
			return candidateSeparator
		}
	}
	return ""
}

func splitAllTitleConnectors(value string) []string {
	for _, connector := range []string{"并且", "以及"} {
		value = strings.ReplaceAll(value, connector, "|")
	}
	value = strings.NewReplacer("且", "|", "与", "|", "和", "|", "及", "|").Replace(value)
	return strings.Split(value, "|")
}

func likelyWordInternalConnector(value, connector string) bool {
	if connector == "和" || strings.Contains(value, "和") {
		return containsAny(value, "和谐", "和平", "和解", "和睦", "和善", "和气", "和好")
	}
	return false
}

func shouldSplitTitle(primaryType KnowledgeType, originalTitle, separator string, parts []string) bool {
	if primaryType == TypeMethodology && isSharedCompositeMethodology(originalTitle, separator, parts) {
		return false
	}
	if primaryType == TypeInsight {
		if isUnifiedInsight(originalTitle, separator, parts) {
			return false
		}
		if !allPartsHaveInsightPredicate(parts) {
			return false
		}
	}
	if separator == "且" || separator == "并且" || separator == "以及" || separator == "|" {
		return true
	}
	return true
}

func isSharedCompositeMethodology(originalTitle, separator string, parts []string) bool {
	if len(parts) != 2 || (separator != "且" && separator != "并且" && separator != "以及" && separator != "与" && separator != "和") {
		return false
	}
	strategyParts := 0
	for _, part := range parts {
		if containsAny(part, "策略", "框架") {
			strategyParts++
		}
	}
	return strategyParts == 1
}

// isUnifiedInsight protects a single insight expressed as two contrasting
// faces. A concession/contrast marker such as "仍然" or "但" is a stronger
// signal of one joined judgment than the conjunction itself; without that
// marker, an explicit "且" boundary remains splittable.
func isUnifiedInsight(originalTitle, separator string, parts []string) bool {
	if len(parts) != 2 || (separator != "且" && separator != "并且" && separator != "以及") {
		return false
	}
	if !containsAny(originalTitle, "仍", "依然", "但是", "但", "却", "而", "反而") {
		return false
	}
	return sharedInsightOpening(parts[0], parts[1])
}

func sharedInsightOpening(left, right string) bool {
	left = insightSubjectPrefix(left)
	right = insightSubjectPrefix(right)
	if left == "" || right == "" {
		return false
	}
	if NormalizeIdentity(left) != "" && NormalizeIdentity(left) == NormalizeIdentity(right) {
		return true
	}
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	if len(leftRunes) < 2 || len(rightRunes) < 2 {
		return false
	}
	// A one-rune shared prefix covers the canonical 智能/智慧 pair while
	// avoiding broader subjects such as 市场/市场份额, which are separate claims.
	return leftRunes[0] == rightRunes[0] && leftRunes[1] != rightRunes[1]
}

func insightSubjectPrefix(value string) string {
	value = strings.TrimSpace(value)
	markerIndex := len(value)
	for _, marker := range []string{"依然", "仍然", "增长", "下降", "稀缺", "通胀", "低估", "高估", "是", "会", "将", "应", "被"} {
		if index := strings.Index(value, marker); index > 0 && index < markerIndex {
			markerIndex = index
		}
	}
	if markerIndex < len(value) {
		return strings.TrimSpace(value[:markerIndex])
	}
	return value
}

func allPartsHaveInsightPredicate(parts []string) bool {
	for _, part := range parts {
		if !containsAny(strings.ToLower(part), "是", "会", "将", "应", "被", "消亡", "低估", "高估", "稀缺", "增长", "下降") {
			return false
		}
	}
	return true
}

func conceptDefinitionSubjects(value string) []string {
	clauses := splitClauses(value)
	if len(clauses) < 2 {
		return nil
	}
	subjects := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		if index := strings.IndexAny(clause, "是为指：:"); index > 0 {
			subject := strings.TrimSpace(clause[:index])
			if len([]rune(subject)) >= 2 {
				subjects = append(subjects, subject)
			}
		}
	}
	if len(subjects) == len(clauses) {
		return subjects
	}
	return nil
}

func splitClaimParts(value string) []string {
	parts, _ := splitClaimPartsWithSeparator(value)
	return parts
}

func splitClaimPartsWithSeparator(value string) ([]string, string) {
	separator := ""
	for _, candidateSeparator := range []string{"且", "；", ";"} {
		if strings.Contains(value, candidateSeparator) {
			separator = candidateSeparator
			break
		}
	}
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(r rune) bool {
		return r == '且' || r == ';' || r == '；'
	})
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = firstClause(part)
		if len([]rune(part)) >= 2 {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) < 2 {
		return nil, separator
	}
	return cleaned, separator
}

func splitClauses(value string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(r rune) bool {
		return r == ';' || r == '；'
	})
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = firstClause(part); part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return cleaned
}

func firstClause(value string) string {
	value = strings.TrimSpace(value)
	for _, separator := range []string{"；", ";", "。", ".", "\n"} {
		if index := strings.Index(value, separator); index > 0 {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func isOrdinaryWordMeaning(candidate ClassifiedKnowledge) bool {
	if candidate.PrimaryType != TypeConcept {
		return false
	}
	title := strings.TrimSpace(candidate.Title)
	definition := strings.TrimSpace(candidate.StructureFields["definition"])
	if obviousOrdinaryMeaning(title) || obviousOrdinaryMeaning(definition) {
		return true
	}
	if len([]rune(stripPunctuation(title))) == 1 && containsHan(title) {
		return true
	}
	return false
}

func obviousOrdinaryMeaning(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "实" || lower == "知道" {
		return true
	}
	return containsAny(lower, "普通词义", "词义解释", "字面意思", "实践含义", "释义", "知道其内容", "一个字")
}

func stripPunctuation(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func splitReason(groups []schemaGroup, titleParts []string) string {
	if len(titleParts) > 1 && len(groups) > 1 {
		return "title_and_structure_multiple_propositions"
	}
	if len(titleParts) > 1 {
		return "title_multiple_propositions"
	}
	return "structure_multiple_types"
}

func buildPendingRelations(parent ClassifiedKnowledge, objects []ClassifiedKnowledge) []PendingRelationDescription {
	if len(objects) < 2 {
		return []PendingRelationDescription{}
	}
	relations := make([]PendingRelationDescription, 0, len(objects)-1)
	for index := 1; index < len(objects); index++ {
		sourceIndex, targetIndex, relationType, description := relationForPair(objects[0], objects[index], parent.Title)
		relations = append(relations, PendingRelationDescription{
			RelationID:        parent.CandidateID + "#relation-" + strconv.Itoa(index),
			SourceCandidateID: objects[sourceIndex].CandidateID,
			TargetCandidateID: objects[targetIndex].CandidateID,
			RelationType:      relationType,
			Description:       description,
			EvidenceIDs:       sortedEvidenceIDs(parent.EvidenceIDs),
		})
	}
	return relations
}

func relationForPair(left, right ClassifiedKnowledge, title string) (int, int, string, string) {
	if left.PrimaryType == TypeCase && right.PrimaryType == TypeInsight {
		return 0, 1, "derived_from", "案例产生该洞察，待 P4 确认目标页面后建立关系"
	}
	if left.PrimaryType == TypeInsight && right.PrimaryType == TypeCase {
		return 1, 0, "derived_from", "案例产生该洞察，待 P4 确认目标页面后建立关系"
	}
	if left.PrimaryType == TypeMethodology && right.PrimaryType == TypeConcept {
		return 0, 1, "explains", "方法论使用并解释该概念，待 P4 确认目标页面后建立关系"
	}
	if left.PrimaryType == TypeConcept && right.PrimaryType == TypeMethodology {
		return 1, 0, "explains", "方法论使用并解释该概念，待 P4 确认目标页面后建立关系"
	}
	if containsAny(strings.ToLower(title), "vs", "之争", "对立", "竞争", "而非", "消亡") {
		return 0, 1, "contradicts", "拆分后的命题存在对立或竞争语义，待 P4 确认目标页面后建立关系"
	}
	return 0, 1, "complements", "拆分后的命题来自同一候选且可独立检索，待 P4 确认目标页面后建立关系"
}

// Validate checks identity, evidence conservation and the P3-only relation
// shape. It explicitly rejects Graph/Wiki page identifiers in relation output.
func (result SplitResult) Validate() error {
	if strings.TrimSpace(result.CandidateID) == "" || strings.TrimSpace(result.OriginalTitle) == "" {
		return fmt.Errorf("split result identity is incomplete")
	}
	if !IsKnowledgeType(result.OriginalPrimaryType) {
		return fmt.Errorf("split result has unsupported original primary_type: %q", result.OriginalPrimaryType)
	}
	if strings.TrimSpace(result.SourceDocumentID) == "" || strings.TrimSpace(result.SourceVideoID) == "" || strings.TrimSpace(result.TranscriptGeneration) == "" {
		return fmt.Errorf("split result source identity is incomplete")
	}
	if result.OriginalPrimaryType == TypeEntity && !IsEntitySubType(result.OriginalEntitySubType) {
		return fmt.Errorf("split result entity_sub_type is required for entity")
	}
	if result.OriginalPrimaryType != TypeEntity && strings.TrimSpace(result.OriginalEntitySubType) != "" {
		return fmt.Errorf("split result entity_sub_type is only valid for entity")
	}
	if len(result.OriginalEvidenceIDs) == 0 {
		return fmt.Errorf("split result original evidence_ids must not be empty")
	}
	if err := validateEvidenceIDs(result.OriginalEvidenceIDs); err != nil {
		return fmt.Errorf("split result: %w", err)
	}
	if result.Status != SplitStatusUnchanged && result.Status != SplitStatusSplit && result.Status != SplitStatusRejected {
		return fmt.Errorf("unsupported split status: %q", result.Status)
	}

	originalEvidence := make(map[string]struct{}, len(result.OriginalEvidenceIDs))
	for _, id := range result.OriginalEvidenceIDs {
		originalEvidence[id] = struct{}{}
	}
	childIDs := make(map[string]struct{}, len(result.Objects))
	accountedEvidence := make(map[string]struct{}, len(result.OriginalEvidenceIDs))
	for _, object := range result.Objects {
		if err := object.Validate(); err != nil {
			return fmt.Errorf("split object %q: %w", object.CandidateID, err)
		}
		if object.SourceDocumentID != result.SourceDocumentID || object.SourceVideoID != result.SourceVideoID || object.TranscriptGeneration != result.TranscriptGeneration {
			return fmt.Errorf("split object %q source identity does not match result", object.CandidateID)
		}
		if _, exists := childIDs[object.CandidateID]; exists {
			return fmt.Errorf("duplicate split object candidate_id %q", object.CandidateID)
		}
		childIDs[object.CandidateID] = struct{}{}
		for _, evidenceID := range object.EvidenceIDs {
			if _, ok := originalEvidence[evidenceID]; !ok {
				return fmt.Errorf("split object %q uses evidence ID %q outside original candidate", object.CandidateID, evidenceID)
			}
			accountedEvidence[evidenceID] = struct{}{}
		}
	}
	for _, rejected := range result.Rejected {
		if strings.TrimSpace(rejected.CandidateID) == "" || strings.TrimSpace(rejected.Title) == "" || strings.TrimSpace(rejected.Reason) == "" {
			return fmt.Errorf("rejected proposition is incomplete")
		}
		if !IsKnowledgeType(rejected.PrimaryType) {
			return fmt.Errorf("rejected proposition has unsupported primary_type: %q", rejected.PrimaryType)
		}
		if err := validateEvidenceIDs(rejected.EvidenceIDs); err != nil {
			return fmt.Errorf("rejected proposition: %w", err)
		}
		for _, evidenceID := range rejected.EvidenceIDs {
			if _, ok := originalEvidence[evidenceID]; !ok {
				return fmt.Errorf("rejected proposition uses evidence ID %q outside original candidate", evidenceID)
			}
			accountedEvidence[evidenceID] = struct{}{}
		}
	}
	if len(accountedEvidence) != len(originalEvidence) {
		return fmt.Errorf("split result loses evidence IDs: got %d of %d", len(accountedEvidence), len(originalEvidence))
	}

	relationIDs := make(map[string]struct{}, len(result.Relations))
	for _, relation := range result.Relations {
		if strings.TrimSpace(relation.RelationID) == "" || strings.TrimSpace(relation.SourceCandidateID) == "" || strings.TrimSpace(relation.TargetCandidateID) == "" || strings.TrimSpace(relation.Description) == "" {
			return fmt.Errorf("pending relation is incomplete")
		}
		if relation.SourceCandidateID == relation.TargetCandidateID {
			return fmt.Errorf("pending relation %q cannot point to itself", relation.RelationID)
		}
		if _, exists := relationIDs[relation.RelationID]; exists {
			return fmt.Errorf("duplicate pending relation ID %q", relation.RelationID)
		}
		relationIDs[relation.RelationID] = struct{}{}
		if _, ok := childIDs[relation.SourceCandidateID]; !ok {
			return fmt.Errorf("pending relation %q source candidate does not exist", relation.RelationID)
		}
		if _, ok := childIDs[relation.TargetCandidateID]; !ok {
			return fmt.Errorf("pending relation %q target candidate does not exist", relation.RelationID)
		}
		if !isPendingRelationType(relation.RelationType) {
			return fmt.Errorf("unsupported pending relation type: %q", relation.RelationType)
		}
		if len(relation.EvidenceIDs) == 0 {
			return fmt.Errorf("pending relation %q evidence_ids must not be empty", relation.RelationID)
		}
		if err := validateEvidenceIDs(relation.EvidenceIDs); err != nil {
			return fmt.Errorf("pending relation %q: %w", relation.RelationID, err)
		}
		for _, evidenceID := range relation.EvidenceIDs {
			if _, ok := originalEvidence[evidenceID]; !ok {
				return fmt.Errorf("pending relation %q uses evidence ID %q outside original candidate", relation.RelationID, evidenceID)
			}
		}
	}

	switch result.Status {
	case SplitStatusUnchanged:
		if len(result.Objects) != 1 || len(result.Relations) != 0 || len(result.Rejected) != 0 {
			return fmt.Errorf("unchanged split result must contain exactly one object and no relations/rejections")
		}
	case SplitStatusSplit:
		if len(result.Objects) < 2 || len(result.Relations) == 0 || len(result.Rejected) != 0 {
			return fmt.Errorf("split result must contain multiple objects and pending relations")
		}
	case SplitStatusRejected:
		if len(result.Objects) != 0 || len(result.Relations) != 0 || len(result.Rejected) == 0 {
			return fmt.Errorf("rejected split result must contain only rejection records")
		}
	}
	return nil
}

func isPendingRelationType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "contradicts", "complements", "explains", "example_of", "derived_from", "part_of":
		return true
	default:
		return false
	}
}

// SortSplitResult normalizes all evidence and relation/object ordering for
// byte-stable persistence in the acceptance artifact.
func SortSplitResult(result *SplitResult) {
	if result == nil {
		return
	}
	result.OriginalEvidenceIDs = sortedEvidenceIDs(result.OriginalEvidenceIDs)
	for index := range result.Objects {
		result.Objects[index].EvidenceIDs = sortedEvidenceIDs(result.Objects[index].EvidenceIDs)
	}
	for index := range result.Relations {
		result.Relations[index].EvidenceIDs = sortedEvidenceIDs(result.Relations[index].EvidenceIDs)
	}
	for index := range result.Rejected {
		result.Rejected[index].EvidenceIDs = sortedEvidenceIDs(result.Rejected[index].EvidenceIDs)
	}
	sort.SliceStable(result.Objects, func(i, j int) bool { return result.Objects[i].CandidateID < result.Objects[j].CandidateID })
	sort.SliceStable(result.Relations, func(i, j int) bool { return result.Relations[i].RelationID < result.Relations[j].RelationID })
	sort.SliceStable(result.Rejected, func(i, j int) bool { return result.Rejected[i].CandidateID < result.Rejected[j].CandidateID })
}
