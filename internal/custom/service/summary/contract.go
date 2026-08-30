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
	ChunkID           string  `json:"chunkId"`
	StartSeconds      float64 `json:"startSeconds"`
	EndSeconds        float64 `json:"endSeconds"`
	Timestamp         string  `json:"timestamp"`
	TranscriptSnippet string  `json:"transcriptSnippet"`
}

type Block struct {
	ID               string     `json:"id"`
	Kind             BlockKind  `json:"kind"`
	Text             string     `json:"text"`
	EvidenceChunkIDs []string   `json:"evidenceChunkIds"`
	Evidence         []Evidence `json:"evidence"`
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
			for evidenceIndex, evidence := range block.Evidence {
				if evidence.ChunkID != block.EvidenceChunkIDs[evidenceIndex] {
					return fmt.Errorf("summary section %q block %q evidence %d does not match its chunk ID", section.Title, block.ID, evidenceIndex+1)
				}
				if evidence.ChunkID == "" || evidence.Timestamp == "" || evidence.TranscriptSnippet == "" || !validTimeRange(evidence.StartSeconds, evidence.EndSeconds) {
					return fmt.Errorf("summary section %q block %q has invalid evidence", section.Title, block.ID)
				}
			}
		}
	}
	return nil
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
			block.Evidence = make([]Evidence, 0, len(block.EvidenceChunkIDs))
			for _, chunkID := range block.EvidenceChunkIDs {
				chunk, exists := chunkByID[chunkID]
				if !exists {
					return fmt.Errorf("resolve unknown evidence chunk %q", chunkID)
				}
				block.Evidence = append(block.Evidence, Evidence{
					ChunkID:           chunk.ID,
					StartSeconds:      float64(chunk.StartMs) / 1000,
					EndSeconds:        float64(chunk.EndMs) / 1000,
					Timestamp:         formatRange(chunk.StartMs, chunk.EndMs),
					TranscriptSnippet: transcriptText(chunk.Content),
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

func transcriptText(content string) string {
	const marker = "## 原文"
	if index := strings.Index(content, marker); index >= 0 {
		return strings.TrimSpace(content[index+len(marker):])
	}
	return strings.TrimSpace(content)
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
