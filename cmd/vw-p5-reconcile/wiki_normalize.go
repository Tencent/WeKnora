package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const legacyDraftType = "legacy_draft"

type wikiRepairScope struct {
	SourceDocumentID string
	EvidenceIDs      map[string]struct{}
	EvidenceAliases  map[string]string
}

type wikiRepairAction struct {
	WikiPageID                 string `json:"wiki_page_id"`
	Slug                       string `json:"slug"`
	Action                     string `json:"action"`
	Reason                     string `json:"reason,omitempty"`
	CanonicalWikiPageID        string `json:"canonical_wiki_page_id,omitempty"`
	CanonicalKnowledgeObjectID string `json:"canonical_knowledge_object_id,omitempty"`

	page  weknora.WikiPage
	write weknora.WikiPageWrite
	type_ knowledge.KnowledgeType
}

type wikiRepairPlan struct {
	Actions    []wikiRepairAction
	IndexWrite weknora.WikiPageWrite
}

func loadWikiRepairScope(ctx context.Context, db *gorm.DB, reader *weknora.Client, knowledgeBaseID string, video model.Video) (wikiRepairScope, error) {
	var source model.VideoTranscriptSource
	if err := db.WithContext(ctx).Where(
		"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
		video.ID, video.TranscriptGeneration, knowledgeBaseID,
	).First(&source).Error; err != nil {
		return wikiRepairScope{}, fmt.Errorf("load active transcript source: %w", err)
	}
	if source.Status != "created" || strings.TrimSpace(source.KnowledgeID) == "" {
		return wikiRepairScope{}, fmt.Errorf("active transcript source is not ready")
	}
	var chunks []model.VideoTranscriptChunk
	if err := db.WithContext(ctx).
		Where("video_id = ? AND generation = ? AND status = ?", video.ID, video.TranscriptGeneration, "completed").
		Find(&chunks).Error; err != nil {
		return wikiRepairScope{}, fmt.Errorf("load active transcript evidence: %w", err)
	}
	evidenceIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		if id := strings.TrimSpace(chunk.EvidenceSentenceID); id != "" {
			evidenceIDs[id] = struct{}{}
		}
	}
	if len(evidenceIDs) == 0 {
		return wikiRepairScope{}, fmt.Errorf("active transcript has no evidence sentence IDs")
	}
	if reader == nil {
		return wikiRepairScope{}, fmt.Errorf("knowledge source reader is not configured")
	}
	sourceChunks, err := reader.ListKnowledgeChunks(ctx, source.KnowledgeID)
	if err != nil {
		return wikiRepairScope{}, fmt.Errorf("read transcript source document: %w", err)
	}
	sourceContent, err := weknora.JoinKnowledgeChunks(sourceChunks)
	if err != nil {
		return wikiRepairScope{}, fmt.Errorf("join transcript source document: %w", err)
	}
	document, err := transcript.ValidateSourceContent(sourceContent, video.ID, video.TranscriptGeneration, video.DurationSeconds)
	if err != nil {
		return wikiRepairScope{}, fmt.Errorf("validate transcript source document: %w", err)
	}
	bySourceSentence := make(map[string]model.VideoTranscriptChunk, len(chunks))
	for _, chunk := range chunks {
		if sourceID := strings.TrimSpace(chunk.SourceSegmentID); sourceID != "" {
			bySourceSentence[sourceID] = chunk
		}
	}
	aliases := make(map[string]string)
	for _, chapter := range document.Chapters {
		for _, paragraph := range chapter.Paragraphs {
			for _, mark := range paragraph.TimeMarks {
				current, ok := bySourceSentence[strings.TrimSpace(mark.SourceSentenceID)]
				if !ok || current.StartMs != mark.StartMs || current.EndMs != mark.EndMs {
					continue
				}
				oldID := strings.TrimSpace(mark.EvidenceSentenceID)
				currentID := strings.TrimSpace(current.EvidenceSentenceID)
				if oldID != "" && currentID != "" {
					aliases[oldID] = currentID
				}
			}
		}
	}
	return wikiRepairScope{
		SourceDocumentID: strings.TrimSpace(source.KnowledgeID), EvidenceIDs: evidenceIDs, EvidenceAliases: aliases,
	}, nil
}

func planWikiRepair(video model.Video, pages []weknora.WikiPage, scope wikiRepairScope) (wikiRepairPlan, error) {
	plan := wikiRepairPlan{Actions: make([]wikiRepairAction, 0)}
	for _, page := range pages {
		if !rawPageBelongsToGeneration(page, video) || !isKnowledgeNamespace(page.Slug) {
			continue
		}
		frontmatter, body, err := parseRepairFrontmatter(page.Content)
		if err == nil && strings.EqualFold(repairString(frontmatter["type"]), legacyDraftType) {
			continue
		}
		write, primaryType, normalizeErr := normalizeWikiObject(page, frontmatter, body, err, video, scope)
		action := wikiRepairAction{WikiPageID: page.ID, Slug: page.Slug, page: page}
		if normalizeErr != nil {
			action.Action = "quarantine"
			action.Reason = normalizeErr.Error()
			action.write = quarantineWikiPage(page, video, normalizeErr.Error())
		} else {
			action.Action = "normalize"
			action.write = write
			action.type_ = primaryType
		}
		plan.Actions = append(plan.Actions, action)
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].Slug < plan.Actions[j].Slug })
	semanticCandidates := make([]knowledge.IdentityCandidate, 0, len(plan.Actions))
	actionIndexes := make([]int, 0, len(plan.Actions))
	for actionIndex := range plan.Actions {
		action := plan.Actions[actionIndex]
		if action.Action != "normalize" {
			continue
		}
		validation, err := knowledge.ValidateWikiObjectPage(action.write.Content, action.write.PageType, video.ID, video.TranscriptGeneration)
		if err != nil {
			return wikiRepairPlan{}, fmt.Errorf("normalized page %s cannot participate in semantic reconciliation: %w", action.Slug, err)
		}
		semanticCandidates = append(semanticCandidates, identityCandidateFromRepair(validation, action.page.Aliases))
		actionIndexes = append(actionIndexes, actionIndex)
	}
	for _, group := range knowledge.GroupSemanticIdentities(semanticCandidates) {
		if len(group) < 2 {
			continue
		}
		winner := group[0]
		for _, candidateIndex := range group[1:] {
			left, right := semanticCandidates[candidateIndex], semanticCandidates[winner]
			if knowledge.CanonicalIdentityLess(left, right) ||
				(!knowledge.CanonicalIdentityLess(right, left) && plan.Actions[actionIndexes[candidateIndex]].WikiPageID < plan.Actions[actionIndexes[winner]].WikiPageID) {
				winner = candidateIndex
			}
		}
		winnerAction := plan.Actions[actionIndexes[winner]]
		winnerIdentity := semanticCandidates[winner]
		for _, candidateIndex := range group {
			if candidateIndex == winner {
				continue
			}
			actionIndex := actionIndexes[candidateIndex]
			action := &plan.Actions[actionIndex]
			action.Action = "quarantine"
			action.Reason = fmt.Sprintf("semantic duplicate of Wiki page %s", winnerAction.WikiPageID)
			action.CanonicalWikiPageID = winnerAction.WikiPageID
			action.CanonicalKnowledgeObjectID = winnerIdentity.KnowledgeObjectID
			action.write = quarantineWikiPageWithCanonical(
				action.page, video, action.Reason, winnerAction.WikiPageID, winnerIdentity.KnowledgeObjectID,
			)
			action.type_ = ""
		}
	}
	valid := make([]wikiRepairAction, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.Action == "normalize" {
			valid = append(valid, action)
		}
	}
	if len(valid) == 0 {
		return wikiRepairPlan{}, fmt.Errorf("no current-generation Wiki page has verifiable evidence")
	}
	plan.IndexWrite = buildVideoIndexWrite(video, scope.SourceDocumentID, valid)
	return plan, nil
}

func identityCandidateFromRepair(validation knowledge.WikiObjectValidation, aliases []string) knowledge.IdentityCandidate {
	return knowledge.IdentityCandidate{
		KnowledgeObjectID: validation.KnowledgeObjectID, KnowledgeType: validation.KnowledgeType,
		EntitySubType: validation.EntitySubType, Title: validation.Title, Aliases: append([]string(nil), aliases...),
		CoreContent: validation.CoreContent, SourceVideoID: validation.SourceVideoID,
		TranscriptGeneration: validation.TranscriptGeneration, StructureFields: validation.StructureFields,
		EvidenceIDs: validation.EvidenceIDs,
	}
}

func normalizeWikiObject(
	page weknora.WikiPage,
	frontmatter map[string]any,
	body string,
	parseErr error,
	video model.Video,
	scope wikiRepairScope,
) (weknora.WikiPageWrite, knowledge.KnowledgeType, error) {
	if parseErr != nil {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("invalid YAML frontmatter: %w", parseErr)
	}
	primaryType := knowledge.KnowledgeType(strings.ToLower(firstRepairString(frontmatter, "primary_type", "type")))
	if !knowledge.IsKnowledgeType(primaryType) {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("unsupported or missing knowledge type")
	}
	if !strings.EqualFold(repairString(frontmatter["audit_status"]), "passed") {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("page was not audited as passed")
	}
	objectID := strings.TrimSpace(repairString(frontmatter["knowledge_object_id"]))
	if objectID == "" {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("knowledge_object_id is missing")
	}
	originalEvidenceIDs := repairStringSlice(frontmatter["evidence_ids"])
	if len(originalEvidenceIDs) == 0 {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("evidence_ids are missing")
	}
	evidenceIDs := make([]string, 0, len(originalEvidenceIDs))
	seenEvidence := make(map[string]struct{}, len(originalEvidenceIDs))
	for _, id := range originalEvidenceIDs {
		currentID := id
		if _, ok := scope.EvidenceIDs[currentID]; !ok {
			currentID = scope.EvidenceAliases[id]
		}
		if _, ok := scope.EvidenceIDs[currentID]; !ok {
			return weknora.WikiPageWrite{}, "", fmt.Errorf("evidence_id %q is not in the active transcript", id)
		}
		if _, duplicate := seenEvidence[currentID]; !duplicate {
			evidenceIDs = append(evidenceIDs, currentID)
			seenEvidence[currentID] = struct{}{}
		}
	}
	sourceRefs := repairStringSlice(frontmatter["source_refs"])
	if len(sourceRefs) != 1 || sourceRefs[0] != scope.SourceDocumentID {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("source_refs do not match the active transcript source")
	}

	rawFields, _ := frontmatter["structure_fields"].(map[string]any)
	entitySubType := ""
	if primaryType == knowledge.TypeEntity {
		entitySubType = inferEntitySubType(frontmatter, rawFields)
		if !knowledge.IsEntitySubType(entitySubType) {
			return weknora.WikiPageWrite{}, "", fmt.Errorf("entity subtype cannot be determined from page metadata")
		}
	}
	fields, err := normalizeStructureFields(primaryType, entitySubType, rawFields)
	if err != nil {
		return weknora.WikiPageWrite{}, "", err
	}
	summary := firstNonEmptyRepairString(
		repairString(frontmatter["summary"]),
		repairString(frontmatter["core_content"]),
		repairString(frontmatter["description"]),
		page.Summary,
		firstBodyParagraph(body),
	)
	if summary == "" {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("page has no reusable core summary")
	}

	canonical := map[string]any{
		"page_type":                 "index",
		"knowledge_object_id":       objectID,
		"type":                      string(primaryType),
		"primary_type":              string(primaryType),
		"source_video_id":           video.ID,
		"transcript_generation":     video.TranscriptGeneration,
		"title":                     strings.TrimSpace(page.Title),
		"summary":                   summary,
		"core_content":              summary,
		"information_nature":        repairInformationNature(primaryType, entitySubType),
		"audit_status":              "passed",
		"classification_confidence": frontmatter["classification_confidence"],
		"evidence_ids":              evidenceIDs,
		"source_refs":               sourceRefs,
		"structure_fields":          fields,
	}
	if primaryType == knowledge.TypeEntity {
		canonical["entity_sub_type"] = entitySubType
	}
	for _, key := range []string{"aliases", "relations", "related_content", "related_atom_ids", "related_entity_ids", "source_atom_ids", "chunk_refs", "time_range"} {
		if value, ok := frontmatter[key]; ok {
			canonical[key] = value
		}
	}
	content, err := renderRepairContent(canonical, body)
	if err != nil {
		return weknora.WikiPageWrite{}, "", err
	}
	if _, err := knowledge.ValidateWikiObjectPage(content, "index", video.ID, video.TranscriptGeneration); err != nil {
		return weknora.WikiPageWrite{}, "", fmt.Errorf("normalized page still fails contract: %w", err)
	}
	return weknora.WikiPageWrite{
		Slug: page.Slug, Title: page.Title, PageType: "index", Status: "published",
		Content: content, Summary: summary, SourceRefs: page.SourceRefs, ChunkRefs: page.ChunkRefs, Version: page.Version,
	}, primaryType, nil
}

func normalizeStructureFields(primaryType knowledge.KnowledgeType, entitySubType string, raw map[string]any) (map[string]string, error) {
	framework, err := knowledge.FrameworkFor(primaryType, entitySubType)
	if err != nil {
		return nil, err
	}
	aliases := repairFieldAliases(primaryType, entitySubType)
	result := make(map[string]string)
	for _, field := range framework.Fields {
		keys := append([]string{field.Key}, aliases[field.Key]...)
		for _, key := range keys {
			if value := repairText(raw[key]); value != "" {
				result[field.Key] = value
				break
			}
		}
	}
	minimum := 2
	if primaryType == knowledge.TypeEntity {
		minimum = 1
	} else if primaryType == knowledge.TypeCase {
		minimum = 3
	}
	if len(result) < minimum {
		return nil, fmt.Errorf("only %d reusable structure fields; %d required", len(result), minimum)
	}
	return result, nil
}

func repairFieldAliases(primaryType knowledge.KnowledgeType, entitySubType string) map[string][]string {
	switch primaryType {
	case knowledge.TypeMethodology:
		return map[string][]string{
			"input": {"prerequisites"}, "steps": {"process"}, "criteria": {"judgment", "standards"},
			"output": {"result"}, "applicability": {"applicable_scenario", "scenario", "conditions"},
		}
	case knowledge.TypeCase:
		return map[string][]string{
			"context": {"scenario", "background", "trigger"}, "actors": {"roles", "participants"},
			"choices": {"decision", "options"}, "actions": {"process", "resolution", "solution"},
			"outcome": {"impact", "result"}, "retrospective": {"lesson", "review"},
		}
	case knowledge.TypeConcept:
		return map[string][]string{
			"definition": {"concept", "description"}, "components": {"key_dimensions", "key_facts", "elements"},
			"mechanism": {"working_principle", "process"}, "distinction": {"boundary", "difference"},
		}
	case knowledge.TypeInsight:
		return map[string][]string{
			"claim": {"insight", "judgment", "key_judgment"}, "reasoning": {"basis", "rationale"},
			"qualifications": {"scope", "conditions", "limitations"}, "implications": {"implication", "suggestion", "recommendation"},
		}
	case knowledge.TypeEntity:
		switch entitySubType {
		case "organization":
			return map[string][]string{
				"org_type": {"organization_type", "entity_type"}, "industry": {"sector"}, "stage": {"scale_signal"},
				"core_business": {"core_activity", "role", "role_in_video"}, "key_people": {"people"},
			}
		case "technology":
			return map[string][]string{
				"tech_category": {"technology_role", "entity_type"}, "application_area": {"capability_summary", "capability_in_video", "role_in_video", "key_facts"},
				"maturity": {"stage"},
			}
		case "person":
			return map[string][]string{"identity": {"role", "role_in_video"}, "background": {"experience"}, "expertise": {"key_facts"}, "standpoint": {"viewpoint"}}
		case "industry":
			return map[string][]string{"scope": {"industry_scope", "entity_type"}, "stage": {"maturity"}, "key_trends": {"trends", "key_facts"}}
		case "place":
			return map[string][]string{"place_type": {"entity_type"}, "associated_activity": {"role", "role_in_video", "key_facts"}}
		default:
			return map[string][]string{
				"product_type": {"product_form", "entity_type", "role"}, "target_users": {"users"},
				"core_function": {"core_features", "capability_in_video", "capability_summary", "key_facts"},
				"tech_basis":    {"technology_basis"}, "differentiation": {"role_in_video", "advantages_in_video"},
			}
		}
	default:
		return nil
	}
}

func inferEntitySubType(frontmatter, fields map[string]any) string {
	if value := strings.ToLower(strings.TrimSpace(repairString(frontmatter["entity_sub_type"]))); knowledge.IsEntitySubType(value) {
		return value
	}
	nature := strings.ToLower(firstNonEmptyRepairString(repairString(frontmatter["information_nature"]), repairString(fields["entity_type"])))
	switch {
	case nature == "人物" || strings.Contains(nature, "person"):
		return "person"
	case nature == "机构" || strings.Contains(nature, "organization") || strings.Contains(nature, "company"):
		return "organization"
	case nature == "技术" || strings.Contains(nature, "technology"):
		return "technology"
	case nature == "行业" || strings.Contains(nature, "industry"):
		return "industry"
	case nature == "地点" || strings.Contains(nature, "place") || strings.Contains(nature, "location"):
		return "place"
	case nature == "产品" || strings.Contains(nature, "product") || strings.Contains(nature, "tool"):
		return "product"
	default:
		return ""
	}
}

func repairInformationNature(primaryType knowledge.KnowledgeType, entitySubType string) string {
	if primaryType == knowledge.TypeEntity {
		return map[string]string{"person": "人物", "organization": "机构", "product": "产品", "technology": "技术", "industry": "行业", "place": "地点"}[entitySubType]
	}
	return map[knowledge.KnowledgeType]string{
		knowledge.TypeMethodology: "方法论", knowledge.TypeCase: "案例", knowledge.TypeConcept: "概念", knowledge.TypeInsight: "洞察",
	}[primaryType]
}

func quarantineWikiPage(page weknora.WikiPage, video model.Video, reason string) weknora.WikiPageWrite {
	return quarantineWikiPageWithCanonical(page, video, reason, "", "")
}

func quarantineWikiPageWithCanonical(page weknora.WikiPage, video model.Video, reason, canonicalWikiPageID, canonicalObjectID string) weknora.WikiPageWrite {
	frontmatter := map[string]any{
		"type": legacyDraftType, "source_video_id": video.ID, "transcript_generation": video.TranscriptGeneration,
		"audit_status": "failed", "original_slug": page.Slug, "original_page_id": page.ID, "repair_reason": reason,
	}
	if strings.TrimSpace(canonicalWikiPageID) != "" {
		frontmatter["canonical_wiki_page_id"] = strings.TrimSpace(canonicalWikiPageID)
	}
	if strings.TrimSpace(canonicalObjectID) != "" {
		frontmatter["canonical_knowledge_object_id"] = strings.TrimSpace(canonicalObjectID)
	}
	content, _ := renderRepairContent(frontmatter, "# "+strings.TrimSpace(page.Title)+"\n\n> 此页保留为未审计草稿，不参与知识图谱投影。\n\n## 原始页面内容\n\n"+page.Content)
	return weknora.WikiPageWrite{
		Slug: page.Slug, Title: page.Title, PageType: "index", Status: "draft", Content: content,
		Summary: page.Summary, SourceRefs: page.SourceRefs, ChunkRefs: page.ChunkRefs, Version: page.Version,
	}
}

func buildVideoIndexWrite(video model.Video, sourceDocumentID string, pages []wikiRepairAction) weknora.WikiPageWrite {
	groups := map[knowledge.KnowledgeType][]wikiRepairAction{}
	for _, page := range pages {
		groups[page.type_] = append(groups[page.type_], page)
	}
	frontmatter := map[string]any{
		"type": "knowledge_base", "source_video_id": video.ID, "transcript_generation": video.TranscriptGeneration,
		"title": strings.TrimSpace(video.Title) + "_知识底座", "audit_status": "aligned", "source_refs": []string{sourceDocumentID},
	}
	var body strings.Builder
	fmt.Fprintf(&body, "# %s_知识底座\n\n", strings.TrimSpace(video.Title))
	fmt.Fprintf(&body, "已审计知识对象：%d 个。\n", len(pages))
	labels := map[knowledge.KnowledgeType]string{
		knowledge.TypeEntity: "实体", knowledge.TypeMethodology: "方法论", knowledge.TypeCase: "案例", knowledge.TypeConcept: "概念", knowledge.TypeInsight: "洞察",
	}
	for _, primaryType := range []knowledge.KnowledgeType{knowledge.TypeEntity, knowledge.TypeMethodology, knowledge.TypeCase, knowledge.TypeConcept, knowledge.TypeInsight} {
		items := groups[primaryType]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&body, "\n## %s（%d）\n\n", labels[primaryType], len(items))
		for _, item := range items {
			fmt.Fprintf(&body, "- [[%s|%s]]\n", item.Slug, item.page.Title)
		}
	}
	content, _ := renderRepairContent(frontmatter, body.String())
	return weknora.WikiPageWrite{
		Slug: "video/" + video.ID, Title: strings.TrimSpace(video.Title) + "_知识底座", PageType: "index", Status: "published",
		Content: content, Summary: fmt.Sprintf("%s 的已审计知识底座，共 %d 个知识对象。", strings.TrimSpace(video.Title), len(pages)),
		SourceRefs: []string{sourceDocumentID},
	}
}

func applyWikiRepair(ctx context.Context, wiki *weknora.WikiClient, knowledgeBaseID string, video model.Video, plan wikiRepairPlan) error {
	for _, action := range plan.Actions {
		written, err := wiki.UpsertPage(ctx, knowledgeBaseID, action.write)
		if err != nil {
			return fmt.Errorf("%s page %s: %w", action.Action, action.Slug, err)
		}
		if written == nil || written.ID != action.WikiPageID {
			return fmt.Errorf("%s page %s returned a different page ID", action.Action, action.Slug)
		}
		readBack, err := wiki.GetPage(ctx, knowledgeBaseID, action.Slug)
		if err != nil || readBack == nil {
			return fmt.Errorf("read back page %s: %w", action.Slug, err)
		}
		if action.Action == "normalize" {
			if _, err := knowledge.ValidateWikiObjectPage(readBack.Content, readBack.PageType, video.ID, video.TranscriptGeneration); err != nil {
				return fmt.Errorf("read back normalized page %s: %w", action.Slug, err)
			}
		} else if !strings.EqualFold(frontmatterString(readBack.ParsedFrontmatter(), "type"), legacyDraftType) || readBack.Status != "draft" {
			return fmt.Errorf("read back quarantined page %s is not an explicit draft", action.Slug)
		}
	}
	index := plan.IndexWrite
	existing, err := wiki.GetPage(ctx, knowledgeBaseID, index.Slug)
	if err != nil {
		return fmt.Errorf("read video index before rebuild: %w", err)
	}
	if existing != nil {
		index.Version = existing.Version
	}
	written, err := wiki.UpsertPage(ctx, knowledgeBaseID, index)
	if err != nil {
		return fmt.Errorf("rebuild video index: %w", err)
	}
	if written == nil || strings.TrimSpace(written.ID) == "" {
		return fmt.Errorf("rebuild video index returned no page")
	}
	return nil
}

func ensureGraphJobIdle(ctx context.Context, db *gorm.DB, video model.Video) error {
	var job model.VideoProcessingJob
	err := db.WithContext(ctx).
		Where("video_id = ? AND job_type = ? AND transcript_generation = ?", video.ID, skill.JobGraph, video.TranscriptGeneration).
		Order("updated_at DESC, created_at DESC").First(&job).Error
	if err != nil && !strings.Contains(err.Error(), "record not found") {
		return err
	}
	if err == nil && (job.Status == "pending" || job.Status == "running") {
		return fmt.Errorf("graph job %s is still %s; wait for the active attempt to stop before writing", job.ID, job.Status)
	}
	return nil
}

func attachWikiRepairPlan(report *reconciliationReport, plan wikiRepairPlan) {
	report.WikiRepairActions = append([]wikiRepairAction(nil), plan.Actions...)
	for _, action := range plan.Actions {
		if action.Action == "normalize" {
			report.NormalizedPageCount++
		} else if action.Action == "quarantine" {
			report.QuarantinedPageCount++
		}
	}
}

func rawPageBelongsToGeneration(page weknora.WikiPage, video model.Video) bool {
	header, _, ok := splitRepairFrontmatter(page.Content)
	if !ok {
		return false
	}
	return rawTopLevelValue(header, "source_video_id") == strings.TrimSpace(video.ID) &&
		rawTopLevelValue(header, "transcript_generation") == strings.TrimSpace(video.TranscriptGeneration)
}

func isKnowledgeNamespace(slug string) bool {
	for _, prefix := range []string{"entity/", "methodology/", "case/", "concept/", "insight/"} {
		if strings.HasPrefix(slug, prefix) {
			return true
		}
	}
	return false
}

func parseRepairFrontmatter(content string) (map[string]any, string, error) {
	header, body, ok := splitRepairFrontmatter(content)
	if !ok {
		return nil, content, fmt.Errorf("frontmatter delimiters are missing")
	}
	frontmatter := map[string]any{}
	if err := yaml.Unmarshal([]byte(header), &frontmatter); err != nil {
		return nil, body, err
	}
	return frontmatter, body, nil
}

func splitRepairFrontmatter(content string) (string, string, bool) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content, false
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.Join(lines[1:index], "\n"), strings.Join(lines[index+1:], "\n"), true
		}
	}
	return "", content, false
}

func rawTopLevelValue(header, key string) string {
	result := ""
	for _, line := range strings.Split(header, "\n") {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		result = strings.Trim(strings.TrimSpace(parts[1]), "\"'")
	}
	return result
}

func renderRepairContent(frontmatter map[string]any, body string) (string, error) {
	header, err := yaml.Marshal(frontmatter)
	if err != nil {
		return "", fmt.Errorf("marshal normalized frontmatter: %w", err)
	}
	return "---\n" + strings.TrimSpace(string(header)) + "\n---\n\n" + strings.TrimSpace(body) + "\n", nil
}

func repairString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func repairStringSlice(value any) []string {
	result := make([]string, 0)
	switch values := value.(type) {
	case []any:
		for _, value := range values {
			if text := repairString(value); text != "" {
				result = append(result, text)
			}
		}
	case []string:
		for _, value := range values {
			if text := strings.TrimSpace(value); text != "" {
				result = append(result, text)
			}
		}
	case string:
		if text := strings.TrimSpace(values); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func repairText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := repairText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "；")
	case []string:
		return strings.Join(typed, "；")
	default:
		return ""
	}
}

func firstRepairString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := repairString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyRepairString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstBodyParagraph(body string) string {
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "#") || strings.HasPrefix(block, "---") || strings.HasPrefix(block, ">") {
			continue
		}
		return strings.Join(strings.Fields(block), " ")
	}
	return ""
}
