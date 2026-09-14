package meetingorchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/promptreload"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gorm.io/gorm"
)

var ErrInsufficientEvidence = errors.New("meeting AI input has no valid evidence")

// A stage may consume at most three provider calls: the initial request, one
// transport retry, and one format/contract correction. Keeping this budget in
// the generator prevents a malformed provider from causing unbounded spend.
const maxMeetingModelCallsPerStage = 3

type MeetingCompletionClient interface {
	CompleteJSONWithSystem(context.Context, string, string) (string, error)
}

// KnowledgeCandidate is metadata-only. The model can select an audited object,
// but it never receives or generates the Wiki body.
type KnowledgeCandidate struct {
	KnowledgeObjectID string `json:"knowledge_object_id"`
	WikiPageID        string `json:"wiki_page_id"`
	KnowledgeType     string `json:"knowledge_type"`
	Title             string `json:"title"`
	AuditStatus       string `json:"audit_status"`
}

type AIProjectionGenerator struct {
	LLM             MeetingCompletionClient
	DB              *gorm.DB
	KnowledgeBaseID string
	EvidenceReader  interface {
		Read(context.Context, string, string) ([]transcript.Chunk, error)
	}
	SummaryReader       SummaryReader
	KnowledgeCandidates []KnowledgeCandidate
}

type evidenceRecord struct {
	VideoID, Generation, Title string
	StartMs, EndMs             int
}
type generatedCluster struct {
	cluster *TopicCluster
	allowed map[string]struct{}
}

func (g *AIProjectionGenerator) Generate(ctx context.Context, videos []model.Video, bundle promptreload.Snapshot) (Projection, error) {
	if g == nil || g.LLM == nil {
		return Projection{}, errors.New("meeting AI client is not configured")
	}
	allowed := map[string]struct{}{}
	evidence := map[string]evidenceRecord{}
	items := make([]map[string]any, 0, len(videos))
	allowedSections := map[string]struct{}{}
	actionableSections := map[string]struct{}{}
	for _, video := range videos {
		ids := make([]string, 0, 24)
		addEvidence := func(id string, record evidenceRecord) {
			id = strings.TrimSpace(id)
			if id == "" {
				return
			}
			if _, exists := allowed[id]; !exists {
				ids = append(ids, id)
			}
			allowed[id] = struct{}{}
			if record.VideoID == "" {
				record.VideoID = video.ID
			}
			if record.Title == "" {
				record.Title = video.Title
			}
			if record.Generation == "" {
				record.Generation = video.TranscriptGeneration
			}
			if _, exists := evidence[id]; !exists {
				evidence[id] = record
			}
		}
		if g.DB != nil && strings.TrimSpace(video.TranscriptGeneration) != "" {
			var chunks []model.VideoTranscriptChunk
			if err := g.DB.WithContext(ctx).Where("video_id = ? AND generation = ? AND status = ?", video.ID, video.TranscriptGeneration, "completed").Order("chunk_index ASC").Find(&chunks).Error; err == nil {
				for _, chunk := range chunks {
					addEvidence(chunk.EvidenceSentenceID, evidenceRecord{VideoID: video.ID, Generation: video.TranscriptGeneration, StartMs: chunk.StartMs, EndMs: chunk.EndMs})
				}
			}
		}
		var transcriptChunks []transcript.Chunk
		if g.EvidenceReader != nil && strings.TrimSpace(video.TranscriptGeneration) != "" {
			if chunks, err := g.EvidenceReader.Read(ctx, video.ID, video.TranscriptGeneration); err == nil {
				transcriptChunks = chunks
				for _, chunk := range chunks {
					addEvidence(chunk.EvidenceSentenceID, evidenceRecord{VideoID: video.ID, Generation: video.TranscriptGeneration, StartMs: chunk.StartMs, EndMs: chunk.EndMs})
				}
			}
		}
		// TranscriptKnowledgeID is only a legacy collection anchor, not a
		// seekable evidence sentence. Never promote it to an evidence ref.
		if len(ids) == 0 {
			continue
		}
		item := map[string]any{"video_id": video.ID, "title": video.Title, "summary_wiki_page_id": video.SummaryWikiPageID, "allowed_evidence": ids}
		if len(transcriptChunks) > 0 {
			bounded := make([]map[string]any, 0, 24)
			for _, chunk := range transcriptChunks {
				if len(bounded) == 24 {
					break
				}
				if _, ok := allowed[chunk.EvidenceSentenceID]; !ok {
					continue
				}
				bounded = append(bounded, map[string]any{"evidence_id": chunk.EvidenceSentenceID, "start_ms": chunk.StartMs, "end_ms": chunk.EndMs, "text": truncateEvidence(chunk.Content, 600)})
			}
			item["evidence"] = bounded
		}
		if g.SummaryReader != nil && strings.TrimSpace(video.SummaryWikiPageID) != "" {
			page, pageErr := g.SummaryReader.GetPageByID(ctx, g.KnowledgeBaseID, video.SummaryWikiPageID)
			if pageErr != nil {
				slog.Warn("meeting summary page read failed", "video_id", video.ID, "reason", "wiki_read_failed")
			} else if page == nil {
				slog.Warn("meeting summary page unavailable", "video_id", video.ID, "reason", "wiki_page_not_found")
			} else {
				if labels := meetingLabels(page.ParsedFrontmatter()); len(labels) > 0 {
					item["meeting_labels"] = labels
				}
				if document, parseErr := summary.ParseStored(page.Content); parseErr == nil {
					sections := make([]map[string]any, 0, len(document.Sections))
					summaryBlockCount := 0
					for _, section := range document.Sections {
						if strings.TrimSpace(section.ID) == "" {
							continue
						}
						allowedSections[section.ID] = struct{}{}
						blocks := make([]map[string]any, 0, len(section.Blocks))
						for _, block := range section.Blocks {
							blockEvidenceIDs := make([]string, 0, len(block.EvidenceRefs))
							for _, ref := range block.EvidenceRefs {
								if strings.TrimSpace(ref.EvidenceSentenceID) != "" {
									addEvidence(ref.EvidenceSentenceID, evidenceRecord{VideoID: video.ID, Generation: video.TranscriptGeneration, StartMs: ref.StartMs, EndMs: ref.EndMs})
									blockEvidenceIDs = append(blockEvidenceIDs, ref.EvidenceSentenceID)
								}
							}
							if summaryBlockCount < 24 && strings.TrimSpace(block.Text) != "" && len(blockEvidenceIDs) > 0 {
								blocks = append(blocks, map[string]any{"block_ref": block.ID, "text": truncateEvidence(block.Text, 1200), "evidence_ids": blockEvidenceIDs})
								if isActionableMeetingSection(section.ID) {
									actionableSections[section.ID] = struct{}{}
								}
								summaryBlockCount++
							}
						}
						sections = append(sections, map[string]any{"section_ref": section.ID, "title": section.Title, "blocks": blocks})
					}
					item["summary_sections"] = sections
					if profile := document.OrchestrationProfile; profile != nil {
						units := make([]map[string]any, 0, len(profile.TopicUnits))
						for _, unit := range profile.TopicUnits {
							unitEvidenceIDs := make([]string, 0, len(unit.EvidenceRefs))
							for _, ref := range unit.EvidenceRefs {
								if strings.TrimSpace(ref.EvidenceSentenceID) == "" {
									continue
								}
								addEvidence(ref.EvidenceSentenceID, evidenceRecord{VideoID: video.ID, Generation: video.TranscriptGeneration, StartMs: ref.StartMs, EndMs: ref.EndMs})
								unitEvidenceIDs = append(unitEvidenceIDs, ref.EvidenceSentenceID)
							}
							units = append(units, map[string]any{"title": unit.Title, "abstract": unit.Abstract, "summary_block_refs": unit.SummaryBlockIDs, "evidence_ids": unitEvidenceIDs})
						}
						item["summary_profile"] = map[string]any{"primary_topic": profile.PrimaryTopic, "topic_units": units}
					}
					slog.Info("meeting summary context prepared", "video_id", video.ID, "sections", len(sections), "profile", document.OrchestrationProfile != nil, "summary_blocks", summaryBlockCount)
				} else {
					slog.Warn("meeting summary page parse failed", "video_id", video.ID, "reason", "summary_contract_parse_failed")
				}
			}
		}
		item["allowed_evidence"] = ids
		items = append(items, item)
	}
	if len(items) == 0 {
		return Projection{}, ErrInsufficientEvidence
	}
	var candidates ItemCandidateOutput
	_, err := g.callJSONValidated(ctx, bundle, "meeting-item-candidate-v1.txt", map[string]any{"videos": items, "allowed_evidence": sortedKeys(allowed), "allowed_section_refs": sortedKeys(allowedSections), "actionable_section_refs": sortedKeys(actionableSections)}, func(raw string) error {
		if err := decodeStrict(raw, &candidates); err != nil {
			return err
		}
		return validateItemCandidates(candidates, allowed, allowedSections, len(actionableSections) > 0)
	})
	if err != nil {
		return Projection{}, err
	}

	clusters := make([]generatedCluster, 0, 8)
	for index, candidate := range candidates.Candidates {
		candidateID := fmt.Sprintf("candidate-%03d", index+1)
		candidateInput := candidatePayload(candidate, candidateID)
		topicCandidates := make([]map[string]any, 0, len(clusters))
		for i, current := range clusters {
			topicCandidates = append(topicCandidates, map[string]any{"topic_id": fmt.Sprintf("topic-%02d", i+1), "business_object": current.cluster.BusinessObject, "scope": current.cluster.Summary, "aliases": []string{}, "allowed_evidence": sortedKeys(current.allowed)})
		}
		var topicMatch TopicMatchOutput
		_, callErr := g.callJSONValidated(ctx, bundle, "meeting-topic-cluster-matching-v1.txt", map[string]any{"candidate": candidateInput, "topic_candidates": topicCandidates, "allowed_evidence": sortedKeys(allowed)}, func(raw string) error {
			if err := decodeStrict(raw, &topicMatch); err != nil {
				return err
			}
			return topicMatch.Validate(allowed)
		})
		if callErr != nil {
			return Projection{}, callErr
		}
		if len(topicMatch.Matches) != 1 {
			return Projection{}, fmt.Errorf("topic match must contain exactly one item")
		}
		match := topicMatch.Matches[0]
		if match.CandidateID != candidateID {
			return Projection{}, fmt.Errorf("topic match candidate identity mismatch")
		}
		candidateAllowed := evidenceIDs(candidate.EvidenceIDs)
		for _, id := range candidate.BusinessObject.EvidenceIDs {
			candidateAllowed[id] = struct{}{}
		}
		for _, id := range candidate.WorkItemScopeEvidenceIDs {
			candidateAllowed[id] = struct{}{}
		}
		for _, id := range candidate.CycleSignalEvidenceIDs {
			candidateAllowed[id] = struct{}{}
		}
		if !allIn(match.CandidateEvidenceIDs, candidateAllowed) {
			return Projection{}, fmt.Errorf("topic match candidate evidence is not from candidate")
		}
		clusterIndex := -1
		if match.ObjectDecision == "possible" {
			// An uncertain object match is a candidate only; it must not create
			// a formal topic cluster or leak into the published projection.
			continue
		}
		if match.ObjectDecision == "same_object" && match.MatchedTopicID != nil {
			clusterIndex = topicIndex(clusters, *match.MatchedTopicID)
			if clusterIndex < 0 {
				return Projection{}, fmt.Errorf("topic match references unknown topic")
			}
			if !allIn(match.MatchedTopicEvidenceIDs, clusters[clusterIndex].allowed) {
				return Projection{}, fmt.Errorf("topic match evidence is not from matched topic")
			}
		}
		if clusterIndex < 0 {
			if candidate.CoreLevel != "core" {
				// Secondary discussion can enrich an existing object, but cannot
				// create a new top-level topic by itself.
				continue
			}
			clusterIndex = len(clusters)
			if clusterIndex >= 8 {
				continue
			}
			clusters = append(clusters, generatedCluster{cluster: &TopicCluster{ClusterID: fmt.Sprintf("topic-%02d", clusterIndex+1), Title: candidate.BusinessObject.Name, BusinessObject: candidate.BusinessObject.Name, Summary: candidate.Reason}, allowed: map[string]struct{}{}})
		}
		current := &clusters[clusterIndex]
		for id := range evidenceIDs(candidate.EvidenceIDs) {
			current.allowed[id] = struct{}{}
		}
		workCandidates := make([]map[string]any, 0, len(current.cluster.WorkItems))
		for _, item := range current.cluster.WorkItems {
			workCandidates = append(workCandidates, map[string]any{"work_item_id": item.ID, "title": item.Title, "scope": "", "cycle_signal": nil, "allowed_evidence": evidenceIDsFromRefs(item.EvidenceRefs)})
		}
		var itemMatch WorkItemMatchOutput
		_, callErr = g.callJSONValidated(ctx, bundle, "meeting-work-item-matching-v1.txt", map[string]any{"candidate": candidateInput, "topic": map[string]any{"topic_id": current.cluster.ClusterID, "business_object": current.cluster.BusinessObject, "allowed_evidence": sortedKeys(current.allowed)}, "work_item_candidates": workCandidates}, func(raw string) error {
			if err := decodeStrict(raw, &itemMatch); err != nil {
				return err
			}
			return itemMatch.Validate(allowed)
		})
		if callErr != nil {
			return Projection{}, callErr
		}
		if len(itemMatch.Matches) != 1 {
			return Projection{}, fmt.Errorf("work item match must contain exactly one item")
		}
		itemDecision := itemMatch.Matches[0]
		if itemDecision.CandidateID != candidateID {
			return Projection{}, fmt.Errorf("work item match candidate identity mismatch")
		}
		if !allIn(itemDecision.CandidateEvidenceIDs, candidateAllowed) {
			return Projection{}, fmt.Errorf("work item match candidate evidence is not from candidate")
		}
		itemID := ""
		if itemDecision.WorkItemDecision == "possible" {
			// Keep uncertain item matches out of the formal branch list until a
			// later meeting supplies enough evidence.
			continue
		}
		if itemDecision.WorkItemDecision == "same_item" && itemDecision.MatchedWorkItemID != nil {
			for i := range current.cluster.WorkItems {
				if current.cluster.WorkItems[i].ID == *itemDecision.MatchedWorkItemID {
					itemID = *itemDecision.MatchedWorkItemID
					break
				}
			}
			if itemID == "" {
				return Projection{}, fmt.Errorf("work item match references unknown item")
			}
			if !allIn(itemDecision.MatchedWorkItemEvidenceIDs, current.allowed) {
				return Projection{}, fmt.Errorf("work item match evidence is not from matched topic")
			}
		}
		if itemID == "" {
			itemID = fmt.Sprintf("item-%02d-%02d", clusterIndex+1, len(current.cluster.WorkItems)+1)
			current.cluster.WorkItems = append(current.cluster.WorkItems, WorkItem{ID: itemID, Title: candidate.SpecificQuestion, Status: "unknown"})
			refs := refsForIDs(candidate.EvidenceIDs, evidence)
			if len(refs) > 0 {
				current.cluster.Evolution = append(current.cluster.Evolution, EvolutionEvent{ID: fmt.Sprintf("event-%02d", len(current.cluster.Evolution)+1), VideoID: refs[0].VideoID, MeetingTitle: evidence[refs[0].EvidenceID].Title, Summary: candidate.Reason, Change: "added", EvidenceRefs: refs})
			}
		}
		itemIndex := workItemIndex(current.cluster.WorkItems, itemID)
		var update WorkItemUpdateOutput
		_, callErr = g.callJSONValidated(ctx, bundle, "meeting-work-item-update-v1.txt", map[string]any{"candidate": candidateInput, "topic_id": current.cluster.ClusterID, "work_item": current.cluster.WorkItems[itemIndex], "existing_decisions": current.cluster.Decisions, "existing_todos": current.cluster.Todos, "allowed_evidence": sortedKeys(allowed)}, func(raw string) error {
			if err := decodeStrict(raw, &update); err != nil {
				return err
			}
			return update.Validate(allowed)
		})
		if callErr != nil {
			return Projection{}, callErr
		}
		current.cluster.WorkItems[itemIndex].EvidenceRefs = appendUniqueRefs(current.cluster.WorkItems[itemIndex].EvidenceRefs, refsForIDs(candidate.EvidenceIDs, evidence)...)
		if update.WorkItemUpdates[0].CandidateID != candidateID || update.WorkItemUpdates[0].TopicID != current.cluster.ClusterID || update.WorkItemUpdates[0].WorkItemRef != itemID {
			return Projection{}, fmt.Errorf("work item update identity mismatch")
		}
		applyUpdate(current.cluster, itemIndex, update.WorkItemUpdates[0], evidence)
	}

	projection := Projection{SchemaVersion: SchemaVersion, TopicClusters: make([]TopicCluster, 0, len(clusters)), TopicRelations: []TopicRelation{}}
	for _, current := range clusters {
		cluster := current.cluster
		cluster.SourceVideoIDs = uniqueVideoIDs(*cluster)
		var selected KnowledgeSelectionOutput
		_, callErr := g.callJSONValidated(ctx, bundle, "meeting-knowledge-selection-v1.txt", map[string]any{"topic": map[string]any{"topic_id": cluster.ClusterID, "business_object": cluster.BusinessObject, "work_items": cluster.WorkItems, "allowed_evidence": sortedKeys(current.allowed)}, "knowledge_candidates": auditedKnowledgeCandidates(g.KnowledgeCandidates), "allowed_evidence": sortedKeys(allowed)}, func(raw string) error {
			if err := decodeStrict(raw, &selected); err != nil {
				return err
			}
			return selected.Validate(allowed)
		})
		if callErr != nil {
			return Projection{}, callErr
		}
		for _, ref := range selected.KnowledgeRefs {
			if ref.TopicID != cluster.ClusterID {
				continue
			}
			found := false
			for _, candidate := range g.KnowledgeCandidates {
				if candidate.KnowledgeObjectID == ref.KnowledgeObjectID && strings.EqualFold(candidate.AuditStatus, "passed") {
					found = true
					cluster.Knowledge = append(cluster.Knowledge, KnowledgeRef{KnowledgeObjectID: candidate.KnowledgeObjectID, WikiPageID: candidate.WikiPageID, KnowledgeType: candidate.KnowledgeType, Title: candidate.Title})
					break
				}
			}
			if !found {
				return Projection{}, fmt.Errorf("knowledge object %q is not in audited whitelist", ref.KnowledgeObjectID)
			}
		}
		projection.TopicClusters = append(projection.TopicClusters, *cluster)
	}
	for i := 0; i < len(projection.TopicClusters); i++ {
		for j := i + 1; j < len(projection.TopicClusters); j++ {
			if err := g.appendRelations(ctx, bundle, &projection, i, j, clusters[i].allowed, clusters[j].allowed, allowed, evidence); err != nil {
				return Projection{}, err
			}
		}
	}
	if len(projection.TopicClusters) <= 1 {
		var empty TopicRelationOutput
		_, callErr := g.callJSONValidated(ctx, bundle, "meeting-topic-relation-v1.txt", map[string]any{"topic_pair": nil, "allowed_evidence": sortedKeys(allowed)}, func(raw string) error {
			if err := decodeStrict(raw, &empty); err != nil {
				return err
			}
			return empty.Validate(allowed)
		})
		if callErr != nil {
			return Projection{}, callErr
		}
	}
	projection.Statistics = projectionStatistics(projection, len(videos), len(items))
	normalizeProjectionArrays(&projection)
	if err := projection.Validate(); err != nil {
		return Projection{}, err
	}
	return projection, nil
}

func meetingLabels(frontmatter map[string]any) []string {
	for _, key := range []string{"meeting_tags", "tags", "labels"} {
		switch raw := frontmatter[key].(type) {
		case []any:
			labels := make([]string, 0, len(raw))
			for _, value := range raw {
				if label, ok := value.(string); ok && strings.TrimSpace(label) != "" {
					labels = append(labels, strings.TrimSpace(label))
				}
			}
			if len(labels) > 0 {
				return labels
			}
		case string:
			if strings.TrimSpace(raw) != "" {
				return []string{strings.TrimSpace(raw)}
			}
		}
	}
	return nil
}

func (g *AIProjectionGenerator) appendRelations(ctx context.Context, bundle promptreload.Snapshot, projection *Projection, left, right int, leftAllowed, rightAllowed, allAllowed map[string]struct{}, evidence map[string]evidenceRecord) error {
	input := map[string]any{"topic_pair": map[string]any{"source": map[string]any{"topic_id": projection.TopicClusters[left].ClusterID, "business_object": projection.TopicClusters[left].BusinessObject, "work_items": projection.TopicClusters[left].WorkItems, "allowed_evidence": sortedKeys(leftAllowed)}, "target": map[string]any{"topic_id": projection.TopicClusters[right].ClusterID, "business_object": projection.TopicClusters[right].BusinessObject, "work_items": projection.TopicClusters[right].WorkItems, "allowed_evidence": sortedKeys(rightAllowed)}}, "allowed_evidence": sortedKeys(allAllowed)}
	var output TopicRelationOutput
	_, err := g.callJSONValidated(ctx, bundle, "meeting-topic-relation-v1.txt", input, func(raw string) error {
		if err := decodeStrict(raw, &output); err != nil {
			return err
		}
		return output.Validate(allAllowed)
	})
	if err != nil {
		return err
	}
	for _, relation := range output.Relations {
		if relation.SourceTopicID != projection.TopicClusters[left].ClusterID || relation.TargetTopicID != projection.TopicClusters[right].ClusterID {
			continue
		}
		if !allIn(relation.SourceEvidenceIDs, leftAllowed) || !allIn(relation.TargetEvidenceIDs, rightAllowed) {
			return fmt.Errorf("relation evidence is on the wrong topic side")
		}
		sourceRefs := refsForIDs(relation.SourceEvidenceIDs, evidence)
		targetRefs := refsForIDs(relation.TargetEvidenceIDs, evidence)
		if len(sourceRefs) == 0 || len(targetRefs) == 0 {
			return fmt.Errorf("relation has no valid two-sided evidence references")
		}
		relationType, mapErr := normalizeTopicRelationType(relation.RelationType)
		if mapErr != nil {
			return mapErr
		}
		projection.TopicRelations = append(projection.TopicRelations, TopicRelation{RelationID: fmt.Sprintf("relation-%02d", len(projection.TopicRelations)+1), SourceClusterID: relation.SourceTopicID, TargetClusterID: relation.TargetTopicID, RelationType: relationType, Summary: relation.Summary, SourceEvidenceRefs: sourceRefs, TargetEvidenceRefs: targetRefs})
	}
	return nil
}

func validateItemCandidates(output ItemCandidateOutput, allowedEvidence, allowedSections map[string]struct{}, required bool) error {
	if err := output.Validate(allowedEvidence); err != nil {
		return err
	}
	if required && len(output.Candidates) == 0 {
		return fmt.Errorf("candidates must not be empty when evidence-backed actionable meeting sections are present")
	}
	for _, candidate := range output.Candidates {
		for _, section := range candidate.CandidateRefs {
			if len(allowedSections) > 0 {
				if _, ok := allowedSections[section]; !ok {
					return fmt.Errorf("summary section %q is not in whitelist", section)
				}
			}
		}
	}
	return nil
}

func isActionableMeetingSection(sectionID string) bool {
	switch strings.TrimSpace(sectionID) {
	case "action-items", "deferred-topics", "important-decisions", "differences-pending-decisions", "actions-next-steps":
		return true
	default:
		return false
	}
}

func (g *AIProjectionGenerator) callJSON(ctx context.Context, bundle promptreload.Snapshot, name string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return g.call(ctx, bundle, name, string(data))
}

// callJSONValidated performs one bounded format/contract correction. The
// callback decodes into the caller's typed value, so successful correction
// never exposes model text to the rest of the generator.
func (g *AIProjectionGenerator) callJSONValidated(ctx context.Context, bundle promptreload.Snapshot, name string, value any, validate func(string) error) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	attempts := 0
	callWithBudget := func(input string) (string, error) {
		var lastErr error
		for attempts < maxMeetingModelCallsPerStage {
			attempts++
			startedAt := time.Now()
			raw, err := g.call(ctx, bundle, name, input)
			if err == nil {
				slog.Info("meeting model stage completed", "stage", name, "attempt", attempts, "duration_ms", time.Since(startedAt).Milliseconds(), "output_bytes", len(raw))
				return raw, nil
			}
			slog.Warn("meeting model stage call failed", "stage", name, "attempt", attempts, "duration_ms", time.Since(startedAt).Milliseconds(), "reason", safeMeetingTransportReason(err))
			lastErr = err
			if !retryableMeetingTransport(err) || attempts >= maxMeetingModelCallsPerStage {
				break
			}
		}
		return "", classifyMeetingTransportError(lastErr)
	}
	raw, err := callWithBudget(string(data))
	if err != nil {
		return "", err
	}
	validationErr := validateMeetingModelOutput(raw, bundle, name)
	if validationErr == nil && validate != nil {
		validationErr = validate(raw)
	}
	if validationErr == nil {
		return raw, nil
	}
	slog.Warn("meeting model stage validation failed", "stage", name, "attempt", attempts, "reason", safeMeetingValidationReason(validationErr))
	if !isCorrectableMeetingValidationError(validationErr) {
		return "", generationError("model_reference_out_of_scope", validationErr)
	}
	correctionInput := string(data) + "\n\nCORRECTION:\n" + correctionSummary(validationErr) + "\n仅返回一个符合 OUTPUT_SCHEMA 的 JSON 对象。"
	corrected, correctionErr := callWithBudget(correctionInput)
	if correctionErr != nil {
		return "", generationError("model_correction_failed", classifyMeetingTransportError(correctionErr))
	}
	if schemaErr := validateMeetingModelOutput(corrected, bundle, name); schemaErr != nil {
		slog.Warn("meeting model stage correction validation failed", "stage", name, "attempt", attempts, "reason", safeMeetingValidationReason(schemaErr))
		return "", generationError("model_correction_failed", schemaErr)
	}
	if validate != nil {
		if contractErr := validate(corrected); contractErr != nil {
			slog.Warn("meeting model stage correction validation failed", "stage", name, "attempt", attempts, "reason", safeMeetingValidationReason(contractErr))
			return "", generationError("model_correction_failed", contractErr)
		}
	}
	return corrected, nil
}

func retryableMeetingTransport(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	// Missing local assets and malformed requests are deterministic failures;
	// retrying them only burns the stage budget.
	if strings.Contains(message, "prompt ") && strings.Contains(message, "missing") {
		return false
	}
	return true
}

func normalizeTopicRelationType(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case RelationPrerequisite:
		return RelationPrerequisite, nil
	case RelationConflictConstraint:
		return RelationConflictConstraint, nil
	case RelationSharedSupport:
		return RelationSharedSupport, nil
	case RelationResultFeedback:
		return RelationResultFeedback, nil
	default:
		return "", fmt.Errorf("invalid relation_type %q", value)
	}
}

func validateMeetingModelOutput(raw string, bundle promptreload.Snapshot, name string) error {
	decoder := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid meeting model JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("invalid meeting model JSON: trailing content")
	}
	if object, ok := value.(map[string]any); !ok || object == nil {
		return fmt.Errorf("invalid meeting model JSON: output must be an object")
	}
	schemaName := strings.TrimSuffix(name, ".txt") + ".schema.json"
	schemaText := strings.TrimSpace(bundle.Schemas[schemaName])
	if schemaText == "" {
		return nil
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaText))
	if err != nil {
		return fmt.Errorf("schema %q is invalid: %w", schemaName, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaName, document); err != nil {
		return fmt.Errorf("schema %q is invalid: %w", schemaName, err)
	}
	schema, err := compiler.Compile(schemaName)
	if err != nil {
		return fmt.Errorf("schema %q is invalid: %w", schemaName, err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("model output does not satisfy schema: %w", err)
	}
	return nil
}
func (g *AIProjectionGenerator) call(ctx context.Context, bundle promptreload.Snapshot, name, input string) (string, error) {
	prompt := strings.TrimSpace(bundle.Prompts[name])
	if prompt == "" {
		return "", fmt.Errorf("meeting prompt %q is missing", name)
	}
	system := prompt
	if schemaName := strings.TrimSuffix(name, ".txt") + ".schema.json"; strings.TrimSpace(bundle.Schemas[schemaName]) != "" {
		system += "\n\nOUTPUT_SCHEMA (machine-readable; do not add fields):\n" + strings.TrimSpace(bundle.Schemas[schemaName])
	}
	return g.LLM.CompleteJSONWithSystem(ctx, system, "INPUT:\n"+input)
}
func candidatePayload(c ItemCandidate, id string) map[string]any {
	candidateAllowed := evidenceIDs(c.EvidenceIDs)
	for _, evidenceID := range c.BusinessObject.EvidenceIDs {
		candidateAllowed[evidenceID] = struct{}{}
	}
	for _, evidenceID := range c.WorkItemScopeEvidenceIDs {
		candidateAllowed[evidenceID] = struct{}{}
	}
	for _, evidenceID := range c.CycleSignalEvidenceIDs {
		candidateAllowed[evidenceID] = struct{}{}
	}
	return map[string]any{"candidate_id": id, "candidate_refs": c.CandidateRefs, "business_object": c.BusinessObject, "specific_question": c.SpecificQuestion, "work_item_scope": c.WorkItemScope, "cycle_signal": c.CycleSignal, "core_level": c.CoreLevel, "reason": c.Reason, "allowed_evidence": sortedKeys(candidateAllowed), "evidence_ids": c.EvidenceIDs}
}
func evidenceIDs(ids []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}
func evidenceIDsFromRefs(refs []EvidenceRef) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.EvidenceID)
	}
	return result
}
func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func topicIndex(clusters []generatedCluster, id string) int {
	for i := range clusters {
		if clusters[i].cluster.ClusterID == id {
			return i
		}
	}
	return -1
}
func workItemIndex(items []WorkItem, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return len(items) - 1
}
func allIn(ids []string, allowed map[string]struct{}) bool {
	for _, id := range ids {
		if _, ok := allowed[id]; !ok {
			return false
		}
	}
	return true
}
func truncateEvidence(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
func refsForIDs(ids []string, evidence map[string]evidenceRecord) []EvidenceRef {
	result := make([]EvidenceRef, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		record, ok := evidence[id]
		if !ok || record.VideoID == "" || record.EndMs <= record.StartMs {
			continue
		}
		result = append(result, EvidenceRef{VideoID: record.VideoID, TranscriptGeneration: record.Generation, EvidenceID: id, StartMs: record.StartMs, EndMs: record.EndMs})
	}
	return result
}
func applyUpdate(cluster *TopicCluster, itemIndex int, update WorkItemUpdate, evidence map[string]evidenceRecord) {
	item := &cluster.WorkItems[itemIndex]
	if update.CurrentStatus != nil {
		item.Status = *update.CurrentStatus
	}
	if update.CurrentConclusion != nil {
		item.CurrentConclusion = *update.CurrentConclusion
	}
	item.EvidenceRefs = appendUniqueRefs(item.EvidenceRefs, refsForIDs(update.CurrentStatusEvidenceIDs, evidence)...)
	item.EvidenceRefs = appendUniqueRefs(item.EvidenceRefs, refsForIDs(update.CurrentConclusionEvidenceIDs, evidence)...)
	if update.Change.IsSubstantive {
		refs := refsForIDs(update.Change.CurrentEvidenceIDs, evidence)
		if len(refs) > 0 {
			cluster.Evolution = append(cluster.Evolution, EvolutionEvent{ID: fmt.Sprintf("event-%02d", len(cluster.Evolution)+1), VideoID: refs[0].VideoID, MeetingTitle: evidence[refs[0].EvidenceID].Title, Summary: deref(update.Change.Discussion), Change: deref(update.Change.ChangeType), EvidenceRefs: refs})
		}
	}
	for _, decision := range update.ImportantDecisions {
		refs := refsForIDs(decision.EvidenceIDs, evidence)
		if len(refs) == 0 {
			continue
		}
		cluster.Decisions = append(cluster.Decisions, Decision{ID: fmt.Sprintf("decision-%02d", len(cluster.Decisions)+1), Text: decision.Content, VideoID: refs[0].VideoID, EvidenceRefs: refs})
	}
	for _, effect := range update.DecisionEffectUpdates {
		for index := range cluster.Decisions {
			if cluster.Decisions[index].ID == effect.DecisionID {
				cluster.Decisions[index].Status = effect.EffectStatusSuggestion
				cluster.Decisions[index].EvidenceRefs = appendUniqueRefs(cluster.Decisions[index].EvidenceRefs, refsForIDs(effect.EffectEvidenceIDs, evidence)...)
				break
			}
		}
	}
	for _, todo := range update.Todos {
		if todo.TodoMatchDecision == "possible" {
			continue
		}
		status := "pending"
		if todo.TodoStatusSuggestion != nil {
			status = *todo.TodoStatusSuggestion
		}
		refs := refsForIDs(todo.EvidenceIDs, evidence)
		if len(refs) == 0 {
			continue
		}
		cluster.Todos = append(cluster.Todos, Todo{ID: fmt.Sprintf("todo-%02d", len(cluster.Todos)+1), Title: todo.Content, Owner: deref(todo.Owner), Due: deref(todo.DueText), Status: status, VideoID: refs[0].VideoID, EvidenceRefs: refs})
	}
}
func appendUniqueRefs(existing []EvidenceRef, refs ...EvidenceRef) []EvidenceRef {
	seen := map[string]struct{}{}
	for _, ref := range existing {
		seen[ref.EvidenceID] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := seen[ref.EvidenceID]; ok {
			continue
		}
		seen[ref.EvidenceID] = struct{}{}
		existing = append(existing, ref)
	}
	return existing
}
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func uniqueVideoIDs(cluster TopicCluster) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, item := range cluster.WorkItems {
		for _, ref := range item.EvidenceRefs {
			if _, ok := seen[ref.VideoID]; !ok {
				seen[ref.VideoID] = struct{}{}
				result = append(result, ref.VideoID)
			}
		}
	}
	for _, event := range cluster.Evolution {
		for _, ref := range event.EvidenceRefs {
			if _, ok := seen[ref.VideoID]; !ok {
				seen[ref.VideoID] = struct{}{}
				result = append(result, ref.VideoID)
			}
		}
	}
	sort.Strings(result)
	return result
}
func auditedKnowledgeCandidates(candidates []KnowledgeCandidate) []KnowledgeCandidate {
	result := make([]KnowledgeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.AuditStatus), "passed") {
			result = append(result, candidate)
		}
	}
	return result
}
func projectionStatistics(projection Projection, scanned, qualified int) Statistics {
	decisions, todos := 0, 0
	for _, cluster := range projection.TopicClusters {
		decisions += len(cluster.Decisions)
		todos += len(cluster.Todos)
	}
	return Statistics{ScannedVideos: scanned, QualifiedVideos: qualified, TopicClusterCount: len(projection.TopicClusters), DecisionCount: decisions, TodoCount: todos}
}
