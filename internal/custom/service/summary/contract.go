package summary

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const SchemaVersion = 1

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

type Document struct {
	SchemaVersion int       `json:"schemaVersion"`
	VideoType     string    `json:"videoType"`
	Sections      []Section `json:"sections"`
}

func (document *Document) UnmarshalJSON(data []byte) error {
	var payload struct {
		SchemaVersion       *int      `json:"schemaVersion"`
		LegacySchemaVersion *int      `json:"schema_version"`
		VideoType           string    `json:"videoType"`
		Sections            []Section `json:"sections"`
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
		{ID: "goals-audience", Title: "一、目标与受众"},
		{ID: "knowledge-map", Title: "二、知识地图"},
		{ID: "core-concepts", Title: "三、核心概念"},
		{ID: "methods-steps", Title: "四、方法与步骤"},
		{ID: "examples-exceptions", Title: "五、示例与异常"},
		{ID: "practice-application", Title: "六、练习与应用"},
	},
	"salon": {
		{ID: "event-participants", Title: "一、活动与参与者"},
		{ID: "topics-views", Title: "二、议题与观点"},
		{ID: "viewpoint-debate", Title: "三、观点交锋"},
		{ID: "cases-qa", Title: "四、案例与问答"},
		{ID: "consensus-differences", Title: "五、共识与分歧"},
		{ID: "exploration-directions", Title: "六、探索方向"},
	},
	"general": {
		{ID: "positioning-problem", Title: "一、定位与问题"},
		{ID: "claims-reasoning", Title: "二、主张与论证"},
		{ID: "evidence-cases", Title: "三、证据与案例"},
		{ID: "limitations-counterarguments", Title: "四、限定与反方"},
		{ID: "impact-recommendations", Title: "五、影响与建议"},
	},
}

func Framework(videoType string) ([]FrameworkSection, bool) {
	framework, ok := frameworks[videoType]
	return framework, ok
}

func NormalizeEvidenceChunkIDs(document *Document, chunks []transcript.Chunk) {
	aliases := make(map[string]string, len(chunks)*2)
	for _, chunk := range chunks {
		aliases[chunk.ID] = chunk.ID
		aliases[fmt.Sprintf("%s|%06d", chunk.ID, chunk.Index)] = chunk.ID
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
	return Parse(stripFrontmatter(content))
}

func ValidateStored(document Document, expectedVideoType string) error {
	if err := Validate(document, expectedVideoType, nil); err != nil {
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
	framework, ok := Framework(expectedVideoType)
	if !ok {
		return fmt.Errorf("unsupported video type: %s", expectedVideoType)
	}
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported summary schema version: %d", document.SchemaVersion)
	}
	if document.VideoType != expectedVideoType {
		return fmt.Errorf("summary video type mismatch: expected %s got %s", expectedVideoType, document.VideoType)
	}
	if len(document.Sections) != len(framework) {
		return fmt.Errorf("summary section count mismatch: expected %d got %d", len(framework), len(document.Sections))
	}

	for sectionIndex, section := range document.Sections {
		expected := framework[sectionIndex]
		if strings.TrimSpace(section.ID) != expected.ID || strings.TrimSpace(section.Title) != expected.Title {
			return fmt.Errorf("summary section %d must be %q", sectionIndex+1, expected.Title)
		}
		if len(section.Blocks) == 0 {
			return fmt.Errorf("summary section %q has no blocks", section.Title)
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
