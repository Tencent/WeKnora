package summary

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const (
	SchemaVersion                     = 2
	legacySchemaVersion               = 1
	OrchestrationProfileSchemaVersion = 1
	maxOrchestrationProfileBytes      = 16000
)

type BlockKind string

const (
	BlockKindParagraph BlockKind = "paragraph"
	BlockKindBullet    BlockKind = "bullet"
)

type Evidence struct {
	ChunkID            string  `json:"chunkId"`
	EvidenceSentenceID string  `json:"evidenceSentenceId,omitempty"`
	StartSeconds       float64 `json:"startSeconds"`
	EndSeconds         float64 `json:"endSeconds"`
	Timestamp          string  `json:"timestamp"`
	TranscriptSnippet  string  `json:"transcriptSnippet"`
}

// EvidenceRef is the stable, machine-readable jump reference for a summary
// block. The rendered Evidence record remains as the frontend compatibility
// view, while this shape is the source-of-truth reference contract.
type EvidenceRef struct {
	ChunkID            string `json:"chunk_id,omitempty"`
	EvidenceSentenceID string `json:"evidence_sentence_id"`
	StartMs            int    `json:"start_ms"`
	EndMs              int    `json:"end_ms"`
}

type Block struct {
	ID               string        `json:"id"`
	Kind             BlockKind     `json:"kind"`
	Text             string        `json:"text"`
	EvidenceChunkIDs []string      `json:"evidenceChunkIds"`
	KnowledgeRefs    []string      `json:"knowledge_refs"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs"`
	Evidence         []Evidence    `json:"evidence"`
}

func (block *Block) UnmarshalJSON(data []byte) error {
	type blockAlias Block
	var payload struct {
		blockAlias
		KnowledgeRefsCamel []string      `json:"knowledgeRefs"`
		EvidenceRefsCamel  []EvidenceRef `json:"evidenceRefs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*block = Block(payload.blockAlias)
	if len(block.KnowledgeRefs) == 0 && payload.KnowledgeRefsCamel != nil {
		block.KnowledgeRefs = payload.KnowledgeRefsCamel
	}
	if len(block.EvidenceRefs) == 0 && payload.EvidenceRefsCamel != nil {
		block.EvidenceRefs = payload.EvidenceRefsCamel
	}
	return nil
}

type Section struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

// Classification records why the current transcript was routed to a summary
// framework. It is optional when reading historical summaries.
type Classification struct {
	Confidence       float64  `json:"confidence"`
	Reason           string   `json:"reason"`
	EvidenceChunkIDs []string `json:"evidenceChunkIds"`
}

// OrchestrationProfile is the bounded routing projection used by the
// cross-video training workflow. The full summary remains the source of truth.
type OrchestrationProfile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	PrimaryTopic  string                   `json:"primaryTopic"`
	TopicUnits    []OrchestrationTopicUnit `json:"topicUnits"`
}

type OrchestrationTopicUnit struct {
	Title            string        `json:"title"`
	Abstract         string        `json:"abstract"`
	ContentForms     []string      `json:"contentForms"`
	LearningOutcomes []string      `json:"learningOutcomes"`
	SummaryBlockIDs  []string      `json:"summaryBlockIds"`
	EvidenceChunkIDs []string      `json:"evidenceChunkIds"`
	EvidenceRefs     []EvidenceRef `json:"evidenceRefs,omitempty"`
}

type Document struct {
	SchemaVersion        int                   `json:"schemaVersion"`
	VideoType            string                `json:"videoType"`
	Classification       *Classification       `json:"classification,omitempty"`
	OrchestrationProfile *OrchestrationProfile `json:"orchestrationProfile,omitempty"`
	Sections             []Section             `json:"sections"`
}

func (document *Document) UnmarshalJSON(data []byte) error {
	var payload struct {
		SchemaVersion        *int                  `json:"schemaVersion"`
		LegacySchemaVersion  *int                  `json:"schema_version"`
		VideoType            string                `json:"videoType"`
		Classification       *Classification       `json:"classification"`
		OrchestrationProfile *OrchestrationProfile `json:"orchestrationProfile"`
		Sections             []Section             `json:"sections"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.SchemaVersion != nil && payload.LegacySchemaVersion != nil && *payload.SchemaVersion != *payload.LegacySchemaVersion {
		return fmt.Errorf("summary schema version fields conflict")
	}
	schemaVersion := payload.SchemaVersion
	if schemaVersion == nil {
		schemaVersion = payload.LegacySchemaVersion
	}
	document.SchemaVersion = 0
	if schemaVersion != nil {
		document.SchemaVersion = *schemaVersion
	}
	document.VideoType = payload.VideoType
	document.Classification = payload.Classification
	document.OrchestrationProfile = payload.OrchestrationProfile
	document.Sections = payload.Sections
	return nil
}

type FrameworkSection struct {
	ID    string
	Title string
}

var frameworks = map[string][]FrameworkSection{
	"interview": {
		{ID: "background", Title: "一、人物背景"},
		{ID: "experience-decisions", Title: "二、经历与决策"},
		{ID: "core-views", Title: "三、核心观点"},
		{ID: "principles-models", Title: "四、原则与思维模型"},
		{ID: "cases-evidence", Title: "五、案例与证据"},
		{ID: "reflection-boundaries", Title: "六、反思与边界"},
	},
	"training": {
		{ID: "learning-goals-audience-prerequisites", Title: "一、学习目标、适用对象与前置知识"},
		{ID: "training-content-system", Title: "二、培训内容体系"},
		{ID: "knowledge-highlights-quotes", Title: "三、核心知识要点与原文金句"},
		{ID: "methods-steps-criteria", Title: "四、方法、操作步骤与判断标准"},
		{ID: "cases-tools-qa", Title: "五、案例、工具使用与互动问答"},
		{ID: "practice-assessment-application", Title: "六、练习、自测与应用清单"},
	},
	"salon": {
		{ID: "event-participants", Title: "一、活动与参与者"},
		{ID: "topics-views", Title: "二、议题与观点"},
		{ID: "viewpoint-debate", Title: "三、观点交锋"},
		{ID: "cases-qa", Title: "四、案例与问答"},
		{ID: "consensus-differences", Title: "五、共识与分歧"},
		{ID: "exploration-directions", Title: "六、探索方向"},
	},
	"meeting": {
		{ID: "meeting-summary", Title: "一、会议总结"},
		{ID: "meeting-basic-information", Title: "二、会议基本信息"},
		{ID: "key-topics-consensus", Title: "三、关键议题和共识"},
		{ID: "meeting-disagreements", Title: "四、会议分歧点"},
		{ID: "discussion-details", Title: "五、会议讨论详情"},
		{ID: "action-items", Title: "六、待办事项"},
		{ID: "deferred-topics", Title: "七、遗留和搁置议题"},
		{ID: "other", Title: "八、其他"},
	},
	"general": {
		{ID: "positioning-problem", Title: "一、定位与问题"},
		{ID: "claims-reasoning", Title: "二、主张与论证"},
		{ID: "evidence-cases", Title: "三、证据与案例"},
		{ID: "limitations-counterarguments", Title: "四、限定与反方"},
		{ID: "impact-recommendations", Title: "五、影响与建议"},
	},
}

var frameworkOrder = []string{"interview", "training", "salon", "meeting", "general"}

var legacyTrainingFramework = []FrameworkSection{
	{ID: "goals-audience", Title: "一、目标与受众"},
	{ID: "knowledge-map", Title: "二、知识地图"},
	{ID: "core-concepts", Title: "三、核心概念"},
	{ID: "methods-steps", Title: "四、方法与步骤"},
	{ID: "examples-exceptions", Title: "五、示例与异常"},
	{ID: "practice-application", Title: "六、练习与应用"},
}

var legacyMeetingFramework = []FrameworkSection{
	{ID: "meeting-goals-participants", Title: "一、会议目标与参与者"},
	{ID: "core-topics-background", Title: "二、核心议题与背景"},
	{ID: "facts-options-constraints", Title: "三、事实、方案与约束"},
	{ID: "important-decisions", Title: "四、重要决策"},
	{ID: "differences-pending-decisions", Title: "五、分歧与待决策事项"},
	{ID: "actions-next-steps", Title: "六、行动项与后续安排"},
	{ID: "results-verification", Title: "七、结果与验证"},
}

func Framework(videoType string) ([]FrameworkSection, bool) {
	framework, ok := frameworks[videoType]
	return framework, ok
}

// Frameworks returns the supported frameworks in a deterministic routing order.
func Frameworks() []struct {
	VideoType string
	Sections  []FrameworkSection
} {
	result := make([]struct {
		VideoType string
		Sections  []FrameworkSection
	}, 0, len(frameworkOrder))
	for _, videoType := range frameworkOrder {
		result = append(result, struct {
			VideoType string
			Sections  []FrameworkSection
		}{VideoType: videoType, Sections: frameworks[videoType]})
	}
	return result
}

func NormalizeEvidenceChunkIDs(document *Document, chunks []transcript.Chunk) {
	aliases := make(map[string]string, len(chunks)*2)
	for _, chunk := range chunks {
		aliases[chunk.ID] = chunk.ID
		aliases[fmt.Sprintf("%s|%06d", chunk.ID, chunk.Index)] = chunk.ID
	}
	if document.Classification != nil {
		for index, chunkID := range document.Classification.EvidenceChunkIDs {
			if normalized, ok := aliases[chunkID]; ok {
				document.Classification.EvidenceChunkIDs[index] = normalized
			}
		}
	}
	if document.OrchestrationProfile != nil {
		for unitIndex := range document.OrchestrationProfile.TopicUnits {
			unit := &document.OrchestrationProfile.TopicUnits[unitIndex]
			for evidenceIndex, chunkID := range unit.EvidenceChunkIDs {
				if normalized, ok := aliases[chunkID]; ok {
					unit.EvidenceChunkIDs[evidenceIndex] = normalized
				}
			}
			for evidenceIndex := range unit.EvidenceRefs {
				if normalized, ok := aliases[unit.EvidenceRefs[evidenceIndex].ChunkID]; ok {
					unit.EvidenceRefs[evidenceIndex].ChunkID = normalized
				}
			}
		}
	}
	for sectionIndex := range document.Sections {
		section := &document.Sections[sectionIndex]
		for blockIndex := range section.Blocks {
			block := &section.Blocks[blockIndex]
			for evidenceIndex, chunkID := range block.EvidenceChunkIDs {
				if normalized, ok := aliases[chunkID]; ok {
					block.EvidenceChunkIDs[evidenceIndex] = normalized
				}
			}
		}
	}
}

func Parse(content string) (Document, error) {
	var document Document
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &document); err != nil {
		return Document{}, fmt.Errorf("parse structured summary JSON: %w", err)
	}
	return document, nil
}

func ParseStored(content string) (Document, error) {
	document, err := Parse(stripFrontmatter(content))
	if err != nil {
		return Document{}, err
	}
	normalizeLegacyTrainingFramework(&document)
	return document, nil
}

func normalizeLegacyTrainingFramework(document *Document) {
	if document.VideoType != "training" || len(document.Sections) != len(legacyTrainingFramework) {
		return
	}
	for index, expected := range legacyTrainingFramework {
		section := document.Sections[index]
		if section.ID != expected.ID || section.Title != expected.Title {
			return
		}
	}
	for index, current := range frameworks["training"] {
		document.Sections[index].ID = current.ID
		document.Sections[index].Title = current.Title
	}
}

func ValidateStored(document Document, expectedVideoType string) error {
	framework, err := storedFramework(document)
	if err != nil {
		return err
	}
	requireClassification := document.SchemaVersion == SchemaVersion
	if err := validateDocument(document, expectedVideoType, nil, framework, requireClassification); err != nil {
		return err
	}
	if err := ValidateOrchestrationProfile(document, nil); err != nil {
		return err
	}
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			if len(block.Evidence) != len(block.EvidenceChunkIDs) {
				return fmt.Errorf("summary section %q block %q has unresolved evidence", section.Title, block.ID)
			}
			if len(block.EvidenceRefs) > 0 && len(block.EvidenceRefs) != len(block.Evidence) {
				return fmt.Errorf("summary section %q block %q has mismatched evidence_refs", section.Title, block.ID)
			}
			for evidenceIndex, evidence := range block.Evidence {
				if evidence.ChunkID != block.EvidenceChunkIDs[evidenceIndex] {
					return fmt.Errorf("summary section %q block %q evidence %d does not match its chunk ID", section.Title, block.ID, evidenceIndex+1)
				}
				if evidence.ChunkID == "" || evidence.EvidenceSentenceID == "" || evidence.Timestamp == "" || evidence.TranscriptSnippet == "" || !validTimeRange(evidence.StartSeconds, evidence.EndSeconds) {
					return fmt.Errorf("summary section %q block %q has invalid evidence", section.Title, block.ID)
				}
				if len(block.EvidenceRefs) > 0 {
					ref := block.EvidenceRefs[evidenceIndex]
					if ref.ChunkID != evidence.ChunkID || ref.EvidenceSentenceID != evidence.EvidenceSentenceID || ref.StartMs != int(math.Round(evidence.StartSeconds*1000)) || ref.EndMs != int(math.Round(evidence.EndSeconds*1000)) {
						return fmt.Errorf("summary section %q block %q evidence_ref %d does not match evidence", section.Title, block.ID, evidenceIndex+1)
					}
				}
			}
		}
	}
	return nil
}

func storedFramework(document Document) ([]FrameworkSection, error) {
	switch document.SchemaVersion {
	case SchemaVersion:
		framework, ok := Framework(strings.TrimSpace(document.VideoType))
		if !ok {
			return nil, fmt.Errorf("unsupported video type: %s", document.VideoType)
		}
		return framework, nil
	case legacySchemaVersion:
		if document.VideoType == "meeting" && len(document.Sections) == len(legacyMeetingFramework) {
			return legacyMeetingFramework, nil
		}
		framework, ok := Framework(strings.TrimSpace(document.VideoType))
		if !ok {
			return nil, fmt.Errorf("unsupported video type: %s", document.VideoType)
		}
		return framework, nil
	default:
		return nil, fmt.Errorf("unsupported summary schema version: %d", document.SchemaVersion)
	}
}

// ValidateEnhancement ensures a knowledge-enhanced summary keeps the
// confirmed foundation structure and transcript evidence anchors intact.
// Knowledge may add context in block text or references, but it cannot move,
// remove, or retarget an already-confirmed summary block.
func ValidateEnhancement(base, enhanced Document) error {
	if base.SchemaVersion != enhanced.SchemaVersion || base.VideoType != enhanced.VideoType {
		return fmt.Errorf("summary enhancement changed schema or video type")
	}
	if len(base.Sections) != len(enhanced.Sections) {
		return fmt.Errorf("summary enhancement changed section count")
	}
	if !sameOrchestrationProfile(base.OrchestrationProfile, enhanced.OrchestrationProfile) {
		return fmt.Errorf("summary enhancement changed orchestration profile")
	}
	for sectionIndex, baseSection := range base.Sections {
		current := enhanced.Sections[sectionIndex]
		if baseSection.ID != current.ID || baseSection.Title != current.Title {
			return fmt.Errorf("summary enhancement changed section %d identity", sectionIndex+1)
		}
		if len(baseSection.Blocks) != len(current.Blocks) {
			return fmt.Errorf("summary enhancement changed block count in section %q", baseSection.Title)
		}
		for blockIndex, baseBlock := range baseSection.Blocks {
			block := current.Blocks[blockIndex]
			if baseBlock.ID != block.ID || baseBlock.Kind != block.Kind {
				return fmt.Errorf("summary enhancement changed block %q identity", baseBlock.ID)
			}
			if !sameStringSet(baseBlock.EvidenceChunkIDs, block.EvidenceChunkIDs) {
				return fmt.Errorf("summary enhancement changed evidence anchors for block %q", baseBlock.ID)
			}
		}
	}
	return nil
}

// PreserveOrchestrationProfile carries the confirmed routing card through an
// enhancement response. The model may omit the program-generated evidence
// refs or repeat the card without those refs, but it may not change the card's
// semantic fields.
func PreserveOrchestrationProfile(base, enhanced *Document) error {
	if base == nil || enhanced == nil || base.OrchestrationProfile == nil {
		return nil
	}
	if enhanced.OrchestrationProfile == nil {
		enhanced.OrchestrationProfile = base.OrchestrationProfile
		return nil
	}
	if sameOrchestrationProfile(base.OrchestrationProfile, enhanced.OrchestrationProfile) {
		enhanced.OrchestrationProfile = base.OrchestrationProfile
		return nil
	}
	if sameOrchestrationProfileWithoutEvidenceRefs(base.OrchestrationProfile, enhanced.OrchestrationProfile) {
		enhanced.OrchestrationProfile = base.OrchestrationProfile
		return nil
	}
	return fmt.Errorf("summary enhancement changed orchestration profile")
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func Validate(document Document, expectedVideoType string, knownChunkIDs map[string]struct{}) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported summary schema version: %d", document.SchemaVersion)
	}
	framework, ok := Framework(strings.TrimSpace(document.VideoType))
	if !ok {
		return fmt.Errorf("unsupported video type: %s", document.VideoType)
	}
	if err := validateDocument(document, expectedVideoType, knownChunkIDs, framework, true); err != nil {
		return err
	}
	return ValidateOrchestrationProfile(document, knownChunkIDs)
}

// ValidateGenerated applies the stricter contract used for a newly generated
// formal summary. Historical summaries intentionally continue to accept a
// missing orchestration profile through ValidateStored.
func ValidateGenerated(document Document, expectedVideoType string, knownChunkIDs map[string]struct{}) error {
	if err := Validate(document, expectedVideoType, knownChunkIDs); err != nil {
		return err
	}
	if document.OrchestrationProfile == nil {
		return fmt.Errorf("generated summary orchestration profile is required")
	}
	return nil
}

// NormalizeOrchestrationProfileReferences makes the profile's evidence
// references a deterministic projection of the summary blocks selected by the
// model. The model is allowed to choose the blocks and their evidence, but it
// must not create a second, independently inconsistent evidence list. Unknown
// blocks and units without any evidence after projection remain hard errors.
func NormalizeOrchestrationProfileReferences(document *Document, knownChunkIDs map[string]struct{}) error {
	if document == nil || document.OrchestrationProfile == nil {
		return nil
	}
	blockEvidence := make(map[string]map[string]struct{})
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			blockID := strings.TrimSpace(block.ID)
			if blockID == "" {
				continue
			}
			evidence := make(map[string]struct{}, len(block.EvidenceChunkIDs))
			for _, chunkID := range block.EvidenceChunkIDs {
				if chunkID = strings.TrimSpace(chunkID); chunkID != "" {
					evidence[chunkID] = struct{}{}
				}
			}
			blockEvidence[blockID] = evidence
		}
	}
	for unitIndex := range document.OrchestrationProfile.TopicUnits {
		unit := &document.OrchestrationProfile.TopicUnits[unitIndex]
		selectedEvidence := make(map[string]struct{})
		for _, blockID := range unit.SummaryBlockIDs {
			blockID = strings.TrimSpace(blockID)
			evidence, ok := blockEvidence[blockID]
			if !ok {
				return fmt.Errorf("orchestration profile topic unit %d references unknown summary block %q", unitIndex+1, blockID)
			}
			for chunkID := range evidence {
				selectedEvidence[chunkID] = struct{}{}
			}
		}
		normalized := make([]string, 0, len(unit.EvidenceChunkIDs))
		seen := make(map[string]struct{}, len(unit.EvidenceChunkIDs))
		for _, chunkID := range unit.EvidenceChunkIDs {
			chunkID = strings.TrimSpace(chunkID)
			if knownChunkIDs != nil {
				if _, ok := knownChunkIDs[chunkID]; !ok {
					return fmt.Errorf("orchestration profile topic unit %d references unknown evidence chunk %q", unitIndex+1, chunkID)
				}
			}
			if _, ok := selectedEvidence[chunkID]; !ok {
				continue
			}
			if _, duplicate := seen[chunkID]; duplicate {
				continue
			}
			seen[chunkID] = struct{}{}
			normalized = append(normalized, chunkID)
		}
		if len(normalized) == 0 {
			return fmt.Errorf("orchestration profile topic unit %d has no evidence in its summary blocks", unitIndex+1)
		}
		unit.EvidenceChunkIDs = normalized
		// Evidence refs are resolved from the canonical transcript chunks after
		// all model output has passed validation.
		unit.EvidenceRefs = nil
	}
	return nil
}

var allowedOrchestrationContentForms = map[string]struct{}{
	"skill_method": {}, "tool_operation": {}, "concept_cognition": {},
	"case_analysis": {}, "humanities_reflection": {}, "process_standard": {},
}

// ValidateOrchestrationProfile validates the optional bounded routing card.
// Missing profiles remain valid for historical summaries.
func ValidateOrchestrationProfile(document Document, knownChunkIDs map[string]struct{}) error {
	profile := document.OrchestrationProfile
	if profile == nil {
		return nil
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("encode orchestration profile: %w", err)
	}
	if len(encoded) > maxOrchestrationProfileBytes {
		return fmt.Errorf("orchestration profile exceeds %d bytes", maxOrchestrationProfileBytes)
	}
	if profile.SchemaVersion != OrchestrationProfileSchemaVersion {
		return fmt.Errorf("unsupported orchestration profile schema version: %d", profile.SchemaVersion)
	}
	if strings.TrimSpace(profile.PrimaryTopic) == "" {
		return fmt.Errorf("orchestration profile primary topic is required")
	}
	if utf8.RuneCountInString(strings.TrimSpace(profile.PrimaryTopic)) > 120 {
		return fmt.Errorf("orchestration profile primary topic is too long")
	}
	if len(profile.TopicUnits) < 1 || len(profile.TopicUnits) > 5 {
		return fmt.Errorf("orchestration profile topic unit count must be between 1 and 5")
	}
	blockIDs := make(map[string]struct{})
	blockEvidenceIDs := make(map[string]map[string]struct{})
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			blockID := strings.TrimSpace(block.ID)
			if blockID == "" {
				return fmt.Errorf("summary contains a block with an empty ID")
			}
			if _, duplicate := blockIDs[blockID]; duplicate {
				return fmt.Errorf("summary contains duplicate block ID %q", blockID)
			}
			blockIDs[blockID] = struct{}{}
			evidenceIDs := make(map[string]struct{}, len(block.EvidenceChunkIDs))
			for _, chunkID := range block.EvidenceChunkIDs {
				chunkID = strings.TrimSpace(chunkID)
				if chunkID != "" {
					evidenceIDs[chunkID] = struct{}{}
				}
			}
			blockEvidenceIDs[blockID] = evidenceIDs
		}
	}
	seenTitles := make(map[string]struct{}, len(profile.TopicUnits))
	seenProfileBlocks := make(map[string]struct{})
	seenProfileEvidence := make(map[string]struct{})
	totalAbstractRunes := 0
	for index, unit := range profile.TopicUnits {
		if strings.TrimSpace(unit.Title) == "" || strings.TrimSpace(unit.Abstract) == "" {
			return fmt.Errorf("orchestration profile topic unit %d requires title and abstract", index+1)
		}
		if utf8.RuneCountInString(strings.TrimSpace(unit.Title)) > 80 {
			return fmt.Errorf("orchestration profile topic unit %d title is too long", index+1)
		}
		if utf8.RuneCountInString(strings.TrimSpace(unit.Abstract)) > 500 {
			return fmt.Errorf("orchestration profile topic unit %d abstract is too long", index+1)
		}
		title := strings.TrimSpace(unit.Title)
		if _, duplicate := seenTitles[title]; duplicate {
			return fmt.Errorf("orchestration profile topic unit %d duplicates title %q", index+1, title)
		}
		seenTitles[title] = struct{}{}
		if len(unit.ContentForms) == 0 || len(unit.LearningOutcomes) == 0 || len(unit.SummaryBlockIDs) == 0 || len(unit.EvidenceChunkIDs) == 0 {
			return fmt.Errorf("orchestration profile topic unit %d is incomplete", index+1)
		}
		seenForms := make(map[string]struct{}, len(unit.ContentForms))
		for _, form := range unit.ContentForms {
			form = strings.TrimSpace(form)
			if form == "" {
				return fmt.Errorf("orchestration profile topic unit %d has an empty content form", index+1)
			}
			if _, duplicate := seenForms[form]; duplicate {
				return fmt.Errorf("orchestration profile topic unit %d duplicates content form %q", index+1, form)
			}
			seenForms[form] = struct{}{}
			if _, ok := allowedOrchestrationContentForms[form]; !ok {
				return fmt.Errorf("orchestration profile topic unit %d has unsupported content form %q", index+1, form)
			}
		}
		seenOutcomes := make(map[string]struct{}, len(unit.LearningOutcomes))
		for _, outcome := range unit.LearningOutcomes {
			outcome = strings.TrimSpace(outcome)
			if outcome == "" {
				return fmt.Errorf("orchestration profile topic unit %d has an empty learning outcome", index+1)
			}
			if _, duplicate := seenOutcomes[outcome]; duplicate {
				return fmt.Errorf("orchestration profile topic unit %d duplicates learning outcome %q", index+1, outcome)
			}
			seenOutcomes[outcome] = struct{}{}
		}
		unitBlocks := make(map[string]struct{}, len(unit.SummaryBlockIDs))
		for _, blockID := range unit.SummaryBlockIDs {
			blockID = strings.TrimSpace(blockID)
			if blockID == "" {
				return fmt.Errorf("orchestration profile topic unit %d has empty summary block ID", index+1)
			}
			if _, duplicate := unitBlocks[blockID]; duplicate {
				return fmt.Errorf("orchestration profile topic unit %d duplicates summary block %q", index+1, blockID)
			}
			unitBlocks[blockID] = struct{}{}
			if _, duplicate := seenProfileBlocks[blockID]; duplicate {
				return fmt.Errorf("orchestration profile reuses summary block %q across topic units", blockID)
			}
			seenProfileBlocks[blockID] = struct{}{}
			if _, ok := blockIDs[blockID]; !ok {
				return fmt.Errorf("orchestration profile topic unit %d references unknown summary block %q", index+1, blockID)
			}
		}
		seenEvidence := make(map[string]struct{}, len(unit.EvidenceChunkIDs))
		for _, chunkID := range unit.EvidenceChunkIDs {
			chunkID = strings.TrimSpace(chunkID)
			if chunkID == "" {
				return fmt.Errorf("orchestration profile topic unit %d has empty evidence chunk ID", index+1)
			}
			if _, duplicate := seenEvidence[chunkID]; duplicate {
				return fmt.Errorf("orchestration profile topic unit %d duplicates evidence chunk %q", index+1, chunkID)
			}
			seenEvidence[chunkID] = struct{}{}
			if _, duplicate := seenProfileEvidence[chunkID]; duplicate {
				return fmt.Errorf("orchestration profile reuses evidence chunk %q across topic units", chunkID)
			}
			seenProfileEvidence[chunkID] = struct{}{}
			belongsToUnitBlock := false
			for blockID := range unitBlocks {
				if _, ok := blockEvidenceIDs[blockID][chunkID]; ok {
					belongsToUnitBlock = true
					break
				}
			}
			if !belongsToUnitBlock {
				return fmt.Errorf("orchestration profile topic unit %d evidence chunk %q is not referenced by its summary blocks", index+1, chunkID)
			}
			if knownChunkIDs != nil {
				if _, ok := knownChunkIDs[chunkID]; !ok {
					return fmt.Errorf("orchestration profile topic unit %d references unknown evidence chunk %q", index+1, chunkID)
				}
			}
		}
		totalAbstractRunes += utf8.RuneCountInString(unit.Abstract)
		if len(unit.EvidenceRefs) > 0 {
			if len(unit.EvidenceRefs) != len(unit.EvidenceChunkIDs) {
				return fmt.Errorf("orchestration profile topic unit %d has mismatched evidence refs", index+1)
			}
			for refIndex, ref := range unit.EvidenceRefs {
				if strings.TrimSpace(ref.ChunkID) == "" || strings.TrimSpace(ref.EvidenceSentenceID) == "" || ref.StartMs < 0 || ref.EndMs <= ref.StartMs || ref.ChunkID != unit.EvidenceChunkIDs[refIndex] {
					return fmt.Errorf("orchestration profile topic unit %d has invalid evidence ref %d", index+1, refIndex+1)
				}
				if knownChunkIDs != nil {
					if _, ok := knownChunkIDs[ref.ChunkID]; !ok {
						return fmt.Errorf("orchestration profile topic unit %d evidence ref %d references unknown chunk %q", index+1, refIndex+1, ref.ChunkID)
					}
				}
			}
		}
	}
	if totalAbstractRunes > 500 {
		return fmt.Errorf("orchestration profile abstracts exceed 500 characters")
	}
	return nil
}

func sameOrchestrationProfile(left, right *OrchestrationProfile) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func sameOrchestrationProfileWithoutEvidenceRefs(left, right *OrchestrationProfile) bool {
	clone := func(profile *OrchestrationProfile) *OrchestrationProfile {
		if profile == nil {
			return nil
		}
		copy := *profile
		copy.TopicUnits = append([]OrchestrationTopicUnit(nil), profile.TopicUnits...)
		for index := range copy.TopicUnits {
			copy.TopicUnits[index].EvidenceRefs = nil
		}
		return &copy
	}
	return sameOrchestrationProfile(clone(left), clone(right))
}

func validateDocument(document Document, expectedVideoType string, knownChunkIDs map[string]struct{}, framework []FrameworkSection, requireClassification bool) error {
	expectedVideoType = strings.TrimSpace(expectedVideoType)
	if expectedVideoType == "" {
		expectedVideoType = strings.TrimSpace(document.VideoType)
	}
	if _, ok := Framework(expectedVideoType); !ok {
		return fmt.Errorf("unsupported video type: %s", expectedVideoType)
	}
	if strings.TrimSpace(document.VideoType) != expectedVideoType {
		return fmt.Errorf("summary video type mismatch: expected %s got %s", expectedVideoType, document.VideoType)
	}
	if requireClassification && document.Classification == nil {
		return fmt.Errorf("summary classification is required for schema version %d", document.SchemaVersion)
	}
	if document.Classification != nil {
		if err := validateClassification(*document.Classification, document.VideoType, knownChunkIDs); err != nil {
			return err
		}
	}
	if len(document.Sections) != len(framework) {
		return fmt.Errorf("summary section count mismatch: expected %d got %d", len(framework), len(document.Sections))
	}

	for sectionIndex, section := range document.Sections {
		expected := framework[sectionIndex]
		if strings.TrimSpace(section.ID) != expected.ID || strings.TrimSpace(section.Title) != expected.Title {
			return fmt.Errorf("summary section %d must be %q", sectionIndex+1, expected.Title)
		}
		for blockIndex, block := range section.Blocks {
			if strings.TrimSpace(block.ID) == "" || strings.TrimSpace(block.Text) == "" {
				return fmt.Errorf("summary section %q block %d is incomplete", section.Title, blockIndex+1)
			}
			if containsMarkup(block.Text) {
				return fmt.Errorf("summary section %q block %d contains Markdown or markup", section.Title, blockIndex+1)
			}
			if block.Kind != BlockKindParagraph && block.Kind != BlockKindBullet {
				return fmt.Errorf("summary section %q block %d has unsupported kind", section.Title, blockIndex+1)
			}
			if len(block.EvidenceChunkIDs) == 0 {
				return fmt.Errorf("summary section %q block %d has no evidence", section.Title, blockIndex+1)
			}
			for _, knowledgeRef := range block.KnowledgeRefs {
				if strings.TrimSpace(knowledgeRef) == "" {
					return fmt.Errorf("summary section %q block %d has an empty knowledge reference", section.Title, blockIndex+1)
				}
			}
			for _, evidenceRef := range block.EvidenceRefs {
				if strings.TrimSpace(evidenceRef.EvidenceSentenceID) == "" || evidenceRef.StartMs < 0 || evidenceRef.EndMs <= evidenceRef.StartMs {
					return fmt.Errorf("summary section %q block %d has an invalid evidence reference", section.Title, blockIndex+1)
				}
			}
			for _, chunkID := range block.EvidenceChunkIDs {
				if strings.TrimSpace(chunkID) == "" {
					return fmt.Errorf("summary section %q block %d has an empty evidence chunk ID", section.Title, blockIndex+1)
				}
				if knownChunkIDs != nil {
					if _, exists := knownChunkIDs[chunkID]; !exists {
						return fmt.Errorf("summary section %q block %d references unknown evidence chunk %q", section.Title, blockIndex+1, chunkID)
					}
				}
			}
		}
	}
	return nil
}

// ValidateClassification requires the routing explanation on newly generated
// summaries while keeping historical summaries readable when the field is absent.
func ValidateClassification(document Document, knownChunkIDs map[string]struct{}) error {
	if document.Classification == nil {
		return fmt.Errorf("summary classification is required for newly generated summaries")
	}
	return validateClassification(*document.Classification, document.VideoType, knownChunkIDs)
}

func validateClassification(classification Classification, videoType string, knownChunkIDs map[string]struct{}) error {
	if math.IsNaN(classification.Confidence) || math.IsInf(classification.Confidence, 0) || classification.Confidence < 0 || classification.Confidence > 1 {
		return fmt.Errorf("summary classification confidence must be between 0 and 1")
	}
	if classification.Confidence < 0.75 && strings.TrimSpace(videoType) != "general" {
		return fmt.Errorf("low-confidence summary classification must use general")
	}
	if strings.TrimSpace(classification.Reason) == "" {
		return fmt.Errorf("summary classification reason is required")
	}
	if len(classification.EvidenceChunkIDs) == 0 {
		return fmt.Errorf("summary classification has no evidence")
	}
	seen := make(map[string]struct{}, len(classification.EvidenceChunkIDs))
	for _, chunkID := range classification.EvidenceChunkIDs {
		chunkID = strings.TrimSpace(chunkID)
		if chunkID == "" {
			return fmt.Errorf("summary classification has an empty evidence chunk ID")
		}
		if _, duplicate := seen[chunkID]; duplicate {
			continue
		}
		seen[chunkID] = struct{}{}
		if knownChunkIDs != nil {
			if _, exists := knownChunkIDs[chunkID]; !exists {
				return fmt.Errorf("summary classification references unknown evidence chunk %q", chunkID)
			}
		}
	}
	if videoType == "meeting" && len(seen) < 2 {
		return fmt.Errorf("meeting classification requires evidence from at least two chunks")
	}
	return nil
}

func ResolveEvidence(document *Document, chunks []transcript.Chunk) error {
	chunkByID := make(map[string]transcript.Chunk, len(chunks))
	for _, chunk := range chunks {
		chunkByID[chunk.ID] = chunk
	}
	for sectionIndex := range document.Sections {
		for blockIndex := range document.Sections[sectionIndex].Blocks {
			block := &document.Sections[sectionIndex].Blocks[blockIndex]
			if block.KnowledgeRefs == nil {
				block.KnowledgeRefs = []string{}
			}
			block.Evidence = make([]Evidence, 0, len(block.EvidenceChunkIDs))
			block.EvidenceRefs = make([]EvidenceRef, 0, len(block.EvidenceChunkIDs))
			for _, chunkID := range block.EvidenceChunkIDs {
				chunk, exists := chunkByID[chunkID]
				if !exists {
					return fmt.Errorf("resolve unknown evidence chunk %q", chunkID)
				}
				if strings.TrimSpace(chunk.EvidenceSentenceID) == "" {
					return fmt.Errorf("resolve evidence chunk %q without immutable sentence ID", chunkID)
				}
				block.Evidence = append(block.Evidence, Evidence{
					ChunkID: chunk.ID, EvidenceSentenceID: chunk.EvidenceSentenceID,
					StartSeconds:      float64(chunk.StartMs) / 1000,
					EndSeconds:        float64(chunk.EndMs) / 1000,
					Timestamp:         formatRange(chunk.StartMs, chunk.EndMs),
					TranscriptSnippet: transcript.OriginalText(chunk.Content),
				})
				block.EvidenceRefs = append(block.EvidenceRefs, EvidenceRef{
					ChunkID: chunk.ID, EvidenceSentenceID: chunk.EvidenceSentenceID,
					StartMs: chunk.StartMs, EndMs: chunk.EndMs,
				})
			}
		}
	}
	if document.OrchestrationProfile != nil {
		for unitIndex := range document.OrchestrationProfile.TopicUnits {
			unit := &document.OrchestrationProfile.TopicUnits[unitIndex]
			unit.EvidenceRefs = make([]EvidenceRef, 0, len(unit.EvidenceChunkIDs))
			for _, chunkID := range unit.EvidenceChunkIDs {
				chunk, exists := chunkByID[chunkID]
				if !exists || strings.TrimSpace(chunk.EvidenceSentenceID) == "" {
					return fmt.Errorf("resolve orchestration evidence chunk %q", chunkID)
				}
				unit.EvidenceRefs = append(unit.EvidenceRefs, EvidenceRef{
					ChunkID: chunk.ID, EvidenceSentenceID: chunk.EvidenceSentenceID,
					StartMs: chunk.StartMs, EndMs: chunk.EndMs,
				})
			}
		}
	}
	return nil
}

func containsMarkup(value string) bool {
	return summaryMarkupPattern.MatchString(value)
}

var summaryMarkupPattern = regexp.MustCompile("(?m)(^|\\n)\\s{0,3}(#{1,6}\\s|[-*+]\\s|\\d+[.)]\\s)|```|\\*\\*|__|~~|\\[[^\\]]+\\]\\([^)]+\\)|<[^>]+>|[<>]")

func formatRange(startMs, endMs int) string {
	return formatTimestamp(startMs) + "–" + formatTimestamp(endMs)
}

func validTimeRange(startSeconds, endSeconds float64) bool {
	return math.IsNaN(startSeconds) == false && math.IsNaN(endSeconds) == false &&
		math.IsInf(startSeconds, 0) == false && math.IsInf(endSeconds, 0) == false &&
		startSeconds >= 0 && endSeconds > startSeconds
}

func formatTimestamp(milliseconds int) string {
	seconds := milliseconds / 1000
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainingSeconds := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, remainingSeconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, remainingSeconds)
}

func stripFrontmatter(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return trimmed
	}
	if end := strings.Index(trimmed[3:], "\n---"); end >= 0 {
		return strings.TrimSpace(trimmed[end+len("\n---")+3:])
	}
	return trimmed
}
