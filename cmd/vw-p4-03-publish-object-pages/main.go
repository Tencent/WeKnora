package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgeprojection"
	"gopkg.in/yaml.v3"
)

type fixtureFile struct {
	Fixtures []fixture `json:"fixtures"`
}

type fixture struct {
	CandidateID          string            `json:"candidate_id"`
	SourceDocumentID     string            `json:"source_document_id"`
	SourceVideoID        string            `json:"source_video_id"`
	TranscriptGeneration string            `json:"transcript_generation"`
	PrimaryType          string            `json:"primary_type"`
	EntitySubType        string            `json:"entity_sub_type"`
	Title                string            `json:"title"`
	CoreContent          string            `json:"core_content"`
	StructureFields      map[string]string `json:"structure_fields"`
	EvidenceIDs          []string          `json:"evidence_ids"`
}

// fieldEvidenceFile is a P3-produced audit artifact. It is deliberately kept
// separate from the object fixture so a page writer cannot infer citations
// from object-level evidence merely because both happen to contain the same ID.
type fieldEvidenceFile struct {
	Mappings []fieldEvidenceRecord `json:"mappings"`
}

type fieldEvidenceRecord struct {
	CandidateID   string              `json:"candidate_id"`
	FieldEvidence map[string][]string `json:"field_evidence"`
}

type evidenceMap struct {
	CandidateID          string   `json:"candidate_id"`
	SourceVideoID        string   `json:"source_video_id"`
	TranscriptGeneration string   `json:"transcript_generation"`
	SourceRefs           []string `json:"source_refs"`
	EvidenceIDs          []string `json:"evidence_ids"`
	ChunkRefs            []string `json:"chunk_refs"`
	TimeRange            string   `json:"time_range"`
}

type sourceProjection struct {
	CandidateID      string `json:"candidate_id"`
	SourceVideoTitle string `json:"source_video_title"`
}

type inputRecord struct {
	CandidateID              string                  `json:"candidate_id"`
	PrimaryType              knowledge.KnowledgeType `json:"primary_type"`
	EntitySubType            string                  `json:"entity_sub_type,omitempty"`
	AuditStatus              string                  `json:"audit_status"`
	ClassificationConfidence float64                 `json:"classification_confidence"`
	SourceVideoID            string                  `json:"source_video_id"`
	SourceVideoTitle         string                  `json:"source_video_title"`
	SourceDocumentID         string                  `json:"source_document_id"`
	TranscriptGeneration     string                  `json:"transcript_generation"`
	EvidenceIDs              []string                `json:"evidence_ids"`
	FieldEvidence            map[string][]string     `json:"field_evidence"`
	SourceRefs               []string                `json:"source_refs"`
	ChunkRefs                []string                `json:"chunk_refs"`
	TimeRange                string                  `json:"time_range"`
	ContentSHA256            string                  `json:"content_sha256"`
	ContentBytes             int                     `json:"content_bytes"`
}

type pageSnapshot struct {
	CandidateID         string   `json:"candidate_id"`
	Exists              bool     `json:"exists"`
	WikiPageID          string   `json:"wiki_page_id,omitempty"`
	Slug                string   `json:"slug,omitempty"`
	PageType            string   `json:"page_type,omitempty"`
	Status              string   `json:"status,omitempty"`
	Version             int      `json:"version,omitempty"`
	ContentSHA256       string   `json:"content_sha256,omitempty"`
	ContentBytes        int      `json:"content_bytes,omitempty"`
	SourceRefs          []string `json:"source_refs,omitempty"`
	ChunkRefs           []string `json:"chunk_refs,omitempty"`
	InLinkCount         int      `json:"in_link_count"`
	OutLinkCount        int      `json:"out_link_count"`
	RelationCount       int      `json:"relation_count"`
	RelatedContentCount int      `json:"related_content_count"`
}

func main() {
	var fixturesPath, evidencePath, sourcePath, fieldEvidencePath, beforePath, inputPath, afterPath, firstPath, secondPath string
	var baseURL, tenantID, kbID, videoID, videoTitle, sourceDocumentID, generation string
	var durationSeconds int64
	var requireEmptyBefore bool
	flag.StringVar(&fixturesPath, "fixtures", "", "P3 fixture JSON")
	flag.StringVar(&evidencePath, "evidence-map", "", "P4-02 evidence NDJSON")
	flag.StringVar(&sourcePath, "source-projection", "", "P4-02 source NDJSON")
	flag.StringVar(&fieldEvidencePath, "field-evidence", "", "P3 field evidence JSON")
	flag.StringVar(&beforePath, "before", "", "redacted before snapshot")
	flag.StringVar(&inputPath, "input", "", "redacted write input")
	flag.StringVar(&afterPath, "after", "", "redacted after snapshot")
	flag.StringVar(&firstPath, "first-results", "", "first publish results")
	flag.StringVar(&secondPath, "second-results", "", "second publish results")
	flag.StringVar(&baseURL, "base-url", "", "WeKnora base URL")
	flag.StringVar(&tenantID, "tenant-id", "", "tenant ID")
	flag.StringVar(&kbID, "kb-id", "", "knowledge base ID")
	flag.StringVar(&videoID, "video-id", "", "active video ID")
	flag.StringVar(&videoTitle, "video-title", "", "active video title")
	flag.StringVar(&sourceDocumentID, "source-document-id", "", "active source document ID")
	flag.StringVar(&generation, "generation", "", "active transcript generation")
	flag.Int64Var(&durationSeconds, "duration-seconds", 0, "active video duration in seconds")
	flag.BoolVar(&requireEmptyBefore, "require-empty-before", false, "require every object page to be absent before the first write")
	flag.Parse()

	for name, value := range map[string]string{"fixtures": fixturesPath, "evidence-map": evidencePath, "source-projection": sourcePath,
		"field-evidence": fieldEvidencePath,
		"before":         beforePath, "input": inputPath, "after": afterPath, "first-results": firstPath, "second-results": secondPath,
		"base-url": baseURL, "tenant-id": tenantID, "kb-id": kbID, "video-id": videoID, "video-title": videoTitle,
		"source-document-id": sourceDocumentID, "generation": generation} {
		if strings.TrimSpace(value) == "" {
			fatalf("%s is required", name)
		}
	}
	if durationSeconds <= 0 {
		fatalf("duration-seconds must be greater than zero")
	}

	fixtures := readFixtures(fixturesPath)
	evidence := readNDJSON[evidenceMap](evidencePath)
	sources := readNDJSON[sourceProjection](sourcePath)
	fieldEvidence := readFieldEvidence(fieldEvidencePath)
	inputs, redacted := buildInputs(fixtures, evidence, sources, fieldEvidence, videoID, videoTitle, sourceDocumentID, generation)
	writeNDJSON(inputPath, redacted)

	client := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: os.Getenv("P4_WEKNORA_API_KEY"), TenantID: tenantID, KnowledgeKBID: kbID})
	ctx := context.Background()
	before := snapshot(ctx, client, kbID, inputs)
	writeNDJSON(beforePath, before)
	if requireEmptyBefore {
		for _, page := range before {
			if page.Exists {
				fatalf("object page %s already exists before first publish; use a clean isolated Wiki for first-write acceptance", page.CandidateID)
			}
		}
	}
	publisher := knowledgeprojection.ObjectPagePublisher{Wiki: client, KBID: kbID, ExpectedVideoID: videoID,
		ExpectedSourceVideoTitle: videoTitle, ExpectedSourceDocumentID: sourceDocumentID,
		ExpectedTranscriptGeneration: generation, ExpectedVideoDurationMs: durationSeconds * 1000}
	first, err := publisher.PublishFirstStage(ctx, inputs)
	if err != nil {
		writeNDJSON(firstPath, first)
		fatalf("first publish: %v", err)
	}
	writeNDJSON(firstPath, first)
	writeNDJSON(afterPath, snapshot(ctx, client, kbID, inputs))
	second, err := publisher.PublishFirstStage(ctx, inputs)
	if err != nil {
		writeNDJSON(secondPath, second)
		fatalf("second publish: %v", err)
	}
	writeNDJSON(secondPath, second)
	if err := verifyRepeatedRun(first, second); err != nil {
		fatalf("idempotency: %v", err)
	}

	actions := map[string]int{}
	for _, result := range first {
		actions[result.Action]++
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "passed", "objects": len(first), "first_actions": actions, "second_all_unchanged": true})
}

func buildInputs(fixtures []fixture, evidence []evidenceMap, sources []sourceProjection, fieldEvidence map[string]map[string][]string, videoID, videoTitle, sourceDocumentID, generation string) ([]knowledgeprojection.ObjectPageInput, []inputRecord) {
	evidenceByID := make(map[string]evidenceMap, len(evidence))
	for _, item := range evidence {
		candidateID := strings.TrimSpace(item.CandidateID)
		if candidateID == "" {
			fatalf("evidence projection candidate_id must not be empty")
		}
		if _, duplicate := evidenceByID[candidateID]; duplicate {
			fatalf("duplicate P4-02 evidence projection for %s", candidateID)
		}
		evidenceByID[candidateID] = item
	}
	sourceByID := make(map[string]sourceProjection, len(sources))
	for _, item := range sources {
		candidateID := strings.TrimSpace(item.CandidateID)
		if candidateID == "" {
			fatalf("source projection candidate_id must not be empty")
		}
		if _, duplicate := sourceByID[candidateID]; duplicate {
			fatalf("duplicate P4-02 source projection for %s", candidateID)
		}
		sourceByID[candidateID] = item
	}
	allEvidence := make([]string, 0, len(fixtures))
	for _, item := range fixtures {
		allEvidence = append(allEvidence, item.EvidenceIDs...)
	}
	fixtureIDs := make(map[string]struct{}, len(fixtures))
	for _, item := range fixtures {
		candidateID := strings.TrimSpace(item.CandidateID)
		if candidateID == "" {
			fatalf("fixture candidate_id must not be empty")
		}
		if _, duplicate := fixtureIDs[candidateID]; duplicate {
			fatalf("duplicate fixture candidate_id %s", candidateID)
		}
		fixtureIDs[candidateID] = struct{}{}
	}
	for candidateID := range evidenceByID {
		if _, ok := fixtureIDs[candidateID]; !ok {
			fatalf("evidence projection contains unknown candidate %s", candidateID)
		}
	}
	for candidateID := range sourceByID {
		if _, ok := fixtureIDs[candidateID]; !ok {
			fatalf("source projection contains unknown candidate %s", candidateID)
		}
	}
	for candidateID := range fieldEvidence {
		if _, ok := fixtureIDs[candidateID]; !ok {
			fatalf("field evidence contains unknown candidate %s", candidateID)
		}
	}
	contextInput := knowledge.DocumentContext{SourceDocumentID: sourceDocumentID, SourceVideoID: videoID,
		TranscriptGeneration: generation, Summary: "P4 acceptance classification context", EvidenceIDs: unique(allEvidence)}
	inputs := make([]knowledgeprojection.ObjectPageInput, 0, len(fixtures))
	redacted := make([]inputRecord, 0, len(fixtures))
	for _, item := range fixtures {
		if len(item.EvidenceIDs) != 1 {
			fatalf("fixture %s needs explicit per-field evidence mapping; only one-evidence fixtures are accepted", item.CandidateID)
		}
		candidate := knowledge.Candidate{ID: item.CandidateID, SourceDocumentID: item.SourceDocumentID, SourceVideoID: item.SourceVideoID,
			TranscriptGeneration: item.TranscriptGeneration, Title: item.Title, CoreContent: item.CoreContent,
			StructureFields: item.StructureFields, EvidenceIDs: item.EvidenceIDs}
		classified, err := knowledge.Classify(candidate, contextInput)
		if err != nil {
			fatalf("classify %s: %v", item.CandidateID, err)
		}
		if string(classified.PrimaryType) != item.PrimaryType || classified.EntitySubType != item.EntitySubType {
			fatalf("classification mismatch for %s", item.CandidateID)
		}
		decision := knowledge.GateClassifiedKnowledge(classified)
		if decision.Status != knowledge.PublishGatePassed || decision.Object == nil {
			fatalf("publish gate rejected %s: %s", item.CandidateID, decision.Reason)
		}
		projected, ok := evidenceByID[item.CandidateID]
		if !ok {
			fatalf("missing P4-02 evidence projection for %s", item.CandidateID)
		}
		source, ok := sourceByID[item.CandidateID]
		if !ok || source.SourceVideoTitle != videoTitle {
			fatalf("source title projection mismatch for %s", item.CandidateID)
		}
		explicitFieldEvidence, ok := fieldEvidence[item.CandidateID]
		if !ok {
			fatalf("missing explicit P3 field evidence for %s", item.CandidateID)
		}
		normalizedFieldEvidence, err := knowledge.NormalizeFirstStageFieldEvidence(*decision.Object, explicitFieldEvidence)
		if err != nil {
			fatalf("invalid P3 field evidence for %s: %v", item.CandidateID, err)
		}
		projection := knowledgeprojection.EvidenceProjection{CandidateID: projected.CandidateID, SourceVideoID: projected.SourceVideoID,
			SourceVideoTitle: source.SourceVideoTitle, TranscriptGeneration: projected.TranscriptGeneration, EvidenceIDs: projected.EvidenceIDs,
			ChunkRefs: projected.ChunkRefs, SourceRefs: projected.SourceRefs, TimeRange: projected.TimeRange}
		publisherInput := knowledgeprojection.ObjectPageInput{Object: *decision.Object, Projection: projection, FieldEvidence: normalizedFieldEvidence}
		render, err := knowledge.RenderFirstStageObjectPage(knowledge.FirstStagePageInput{Object: publisherInput.Object,
			TimeRange: projection.TimeRange, ChunkRefs: projection.ChunkRefs, FieldEvidence: normalizedFieldEvidence})
		if err != nil {
			fatalf("render redacted input digest for %s: %v", item.CandidateID, err)
		}
		contentDigest := sha256.Sum256([]byte(render.Content))
		inputs = append(inputs, publisherInput)
		redacted = append(redacted, inputRecord{CandidateID: item.CandidateID, PrimaryType: decision.Object.PrimaryType,
			EntitySubType: decision.Object.EntitySubType, AuditStatus: decision.Object.AuditStatus,
			ClassificationConfidence: decision.Object.ClassificationConfidence, SourceVideoID: videoID, SourceVideoTitle: videoTitle,
			SourceDocumentID: sourceDocumentID, TranscriptGeneration: generation, EvidenceIDs: projected.EvidenceIDs,
			FieldEvidence: normalizedFieldEvidence, SourceRefs: projected.SourceRefs, ChunkRefs: projected.ChunkRefs, TimeRange: projected.TimeRange,
			ContentSHA256: hex.EncodeToString(contentDigest[:]), ContentBytes: len([]byte(render.Content))})
	}
	return inputs, redacted
}

func readFieldEvidence(path string) map[string]map[string][]string {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open field evidence: %v", err)
	}
	defer file.Close()
	var payload fieldEvidenceFile
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		fatalf("decode field evidence: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			fatalf("field evidence contains multiple JSON values")
		}
		fatalf("decode trailing field evidence: %v", err)
	}
	if len(payload.Mappings) == 0 {
		fatalf("field evidence mappings must not be empty")
	}
	result := make(map[string]map[string][]string, len(payload.Mappings))
	for _, mapping := range payload.Mappings {
		candidateID := strings.TrimSpace(mapping.CandidateID)
		if candidateID == "" {
			fatalf("field evidence candidate_id must not be empty")
		}
		if _, duplicate := result[candidateID]; duplicate {
			fatalf("duplicate field evidence candidate_id %s", candidateID)
		}
		if mapping.FieldEvidence == nil {
			fatalf("field evidence for %s must be an object", candidateID)
		}
		result[candidateID] = mapping.FieldEvidence
	}
	return result
}

func snapshot(ctx context.Context, client *weknora.WikiClient, kbID string, inputs []knowledgeprojection.ObjectPageInput) []pageSnapshot {
	pages, err := client.ListAllPages(ctx, kbID, "")
	if err != nil {
		fatalf("list Wiki pages for snapshot: %v", err)
	}
	result := make([]pageSnapshot, 0, len(inputs))
	for _, input := range inputs {
		matches := make([]weknora.WikiPage, 0, 1)
		for _, page := range pages {
			fm := frontmatter(page.Content)
			if stringValue(fm["knowledge_object_id"]) == input.Object.CandidateID &&
				stringValue(fm["source_video_id"]) == input.Object.SourceVideoID &&
				stringValue(fm["transcript_generation"]) == input.Object.TranscriptGeneration {
				matches = append(matches, page)
			}
		}
		if len(matches) > 1 {
			fatalf("duplicate Wiki pages in snapshot for %s", input.Object.CandidateID)
		}
		if len(matches) == 0 {
			result = append(result, pageSnapshot{CandidateID: input.Object.CandidateID})
			continue
		}
		page, err := client.GetPage(ctx, kbID, matches[0].Slug)
		if err != nil || page == nil {
			fatalf("read Wiki page snapshot for %s: %v", input.Object.CandidateID, err)
		}
		digest := sha256.Sum256([]byte(page.Content))
		fm := frontmatter(page.Content)
		result = append(result, pageSnapshot{CandidateID: input.Object.CandidateID, Exists: true, WikiPageID: page.ID,
			Slug: page.Slug, PageType: page.PageType, Status: page.Status, Version: page.Version,
			ContentSHA256: hex.EncodeToString(digest[:]), ContentBytes: len([]byte(page.Content)),
			SourceRefs: page.SourceRefs, ChunkRefs: page.ChunkRefs, InLinkCount: len(page.InLinks), OutLinkCount: len(page.OutLinks),
			RelationCount: listLength(fm["relations"]), RelatedContentCount: listLength(fm["related_content"])})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CandidateID < result[j].CandidateID })
	return result
}

func verifyRepeatedRun(first, second []knowledgeprojection.ObjectPageResult) error {
	if len(first) == 0 || len(first) != len(second) {
		return fmt.Errorf("result count mismatch")
	}
	for i := range first {
		if second[i].Action != "unchanged" || first[i].CandidateID != second[i].CandidateID || first[i].WikiPageID != second[i].WikiPageID ||
			first[i].Slug != second[i].Slug || first[i].Version != second[i].Version || first[i].ContentSHA256 != second[i].ContentSHA256 {
			return fmt.Errorf("object %s changed on repeated publish", first[i].CandidateID)
		}
	}
	return nil
}

func readFixtures(path string) []fixture {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open fixtures: %v", err)
	}
	defer file.Close()
	var value fixtureFile
	if err := json.NewDecoder(file).Decode(&value); err != nil {
		fatalf("decode fixtures: %v", err)
	}
	return value.Fixtures
}

func readNDJSON[T any](path string) []T {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	result := make([]T, 0)
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		var value T
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			fatalf("decode %s: %v", path, err)
		}
		result = append(result, value)
	}
	if err := scanner.Err(); err != nil {
		fatalf("scan %s: %v", path, err)
	}
	return result
}

func writeNDJSON[T any](path string, values []T) {
	file, err := os.Create(path)
	if err != nil {
		fatalf("create %s: %v", path, err)
	}
	encoder := json.NewEncoder(file)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			_ = file.Close()
			fatalf("encode %s: %v", path, err)
		}
	}
	if err := file.Close(); err != nil {
		fatalf("close %s: %v", path, err)
	}
}

func frontmatter(content string) map[string]any {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]any{}
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	result := map[string]any{}
	if end < 0 || yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &result) != nil {
		return map[string]any{}
	}
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}

func listLength(value any) int {
	values, _ := value.([]any)
	return len(values)
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[p4-03] ERROR: "+format+"\n", args...)
	os.Exit(1)
}
