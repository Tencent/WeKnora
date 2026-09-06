package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgeprojection"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

type evidenceRow struct {
	EvidenceSentenceID string
	ChunkID            string
	StartMs            int
	EndMs              int
}

func main() {
	var baseURL, apiKey, tenantID, sourceKB, objectKB, videoID, generation, videoType string
	var summarySlug, pageMapPath, evidencePath, outputDir string
	flag.StringVar(&baseURL, "base-url", "", "WeKnora base URL")
	flag.StringVar(&apiKey, "api-key", os.Getenv("P4_WEKNORA_API_KEY"), "WeKnora API key")
	flag.StringVar(&tenantID, "tenant-id", "10000", "WeKnora tenant ID")
	flag.StringVar(&sourceKB, "summary-kb-id", "", "KB containing the active summary")
	flag.StringVar(&objectKB, "object-kb-id", "", "isolated KB containing P4-03 object pages")
	flag.StringVar(&videoID, "video-id", "", "active video ID")
	flag.StringVar(&generation, "generation", "", "active transcript generation")
	flag.StringVar(&videoType, "video-type", "", "video type")
	flag.StringVar(&summarySlug, "summary-slug", "", "summary Wiki slug")
	flag.StringVar(&pageMapPath, "page-map", "", "P4-03 page ID map TSV")
	flag.StringVar(&evidencePath, "evidence", "", "current-generation evidence TSV")
	flag.StringVar(&outputDir, "output-dir", "", "acceptance output directory")
	flag.Parse()
	for name, value := range map[string]string{"base-url": baseURL, "summary-kb-id": sourceKB, "object-kb-id": objectKB, "video-id": videoID, "generation": generation, "video-type": videoType, "summary-slug": summarySlug, "page-map": pageMapPath, "evidence": evidencePath, "output-dir": outputDir} {
		if strings.TrimSpace(value) == "" {
			fatalf("%s is required", name)
		}
	}
	client := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, TenantID: tenantID})
	ctx := context.Background()
	summaryPage, err := client.GetPage(ctx, sourceKB, summarySlug)
	if err != nil || summaryPage == nil {
		fatalf("read summary page: %v", firstError(err, fmt.Errorf("page not found")))
	}
	draft, err := summary.ParseStored(summaryPage.Content)
	if err != nil {
		fatalf("parse summary page: %v", err)
	}
	if strings.TrimSpace(draft.VideoType) != videoType {
		fatalf("summary video type mismatch: %s", draft.VideoType)
	}
	if !strings.Contains(summaryPage.Content, "transcript_generation: "+generation) {
		fatalf("summary page does not belong to active transcript generation")
	}
	pages := readPageMap(pageMapPath, videoID, generation)
	evidence := readEvidence(evidencePath, videoID, generation)
	allPages, err := client.ListAllPages(ctx, objectKB, "")
	if err != nil {
		fatalf("list object pages: %v", err)
	}
	scope, bindings := buildScope(allPages, pages, evidence, videoID, generation)
	coverage, err := knowledgeprojection.AuditSummaryKnowledgeCoverage(draft, bindings, scope)
	if err != nil {
		fatalf("audit summary knowledge coverage: %v", err)
	}
	beforeResult, err := knowledgeprojection.ValidateSummaryDoubleReference(&draft, nil, scope, videoType)
	if err != nil {
		fatalf("audit summary draft: %v", err)
	}
	candidate := cloneDocument(draft)
	bindErr := knowledgeprojection.BindSummaryKnowledgeReferences(&candidate, bindings, scope)
	afterResult, validationErr := knowledgeprojection.ValidateSummaryDoubleReference(&draft, &candidate, scope, videoType)
	writeArtifacts(outputDir, summaryPage, draft, candidate, beforeResult, afterResult, bindErr, validationErr, coverage, scope, bindings, videoID, generation, videoType)
	fmt.Printf("summary audit complete: draft_blocks=%d final_promotable=%t fallback=%t\n", countAudits(beforeResult.DraftAudits), afterResult.FinalPromotable, afterResult.FallbackRequired)
}

func buildScope(allPages []weknora.WikiPage, pageResults []knowledgeprojection.ObjectPageResult, evidence map[string]knowledgeprojection.EvidenceReferenceProjection, videoID, generation string) (knowledgeprojection.SummaryReferenceScope, []knowledgeprojection.SummaryKnowledgeBinding) {
	pageByID := make(map[string]knowledgeprojection.ObjectPageResult, len(pageResults))
	for _, page := range pageResults {
		pageByID[page.WikiPageID] = page
	}
	scope := knowledgeprojection.SummaryReferenceScope{VideoID: videoID, TranscriptGeneration: generation, Pages: pageByID, Evidence: evidence}
	bindings := make([]knowledgeprojection.SummaryKnowledgeBinding, 0)
	for _, page := range allPages {
		if _, ok := pageByID[page.ID]; !ok {
			continue
		}
		fm := page.ParsedFrontmatter()
		if strings.TrimSpace(stringValue(fm["source_video_id"])) != videoID || strings.TrimSpace(stringValue(fm["transcript_generation"])) != generation || strings.ToLower(strings.TrimSpace(stringValue(fm["audit_status"]))) != "passed" {
			continue
		}
		bindings = append(bindings, knowledgeprojection.SummaryKnowledgeBinding{WikiPageID: page.ID, EvidenceIDs: stringSlice(fm["evidence_ids"]), ChunkRefs: stringSlice(fm["chunk_refs"])})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].WikiPageID < bindings[j].WikiPageID })
	return scope, bindings
}

func readPageMap(path, videoID, generation string) []knowledgeprojection.ObjectPageResult {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open page map: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		fatalf("page map is empty")
	}
	pages := make([]knowledgeprojection.ObjectPageResult, 0)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 7 {
			fatalf("page map row has fewer than 7 columns")
		}
		version, err := strconv.Atoi(fields[6])
		if err != nil {
			fatalf("invalid page version: %v", err)
		}
		pages = append(pages, knowledgeprojection.ObjectPageResult{CandidateID: fields[0], WikiPageID: fields[4], Slug: fields[5], Version: version, SourceVideoID: videoID, TranscriptGeneration: generation})
	}
	if err := scanner.Err(); err != nil {
		fatalf("read page map: %v", err)
	}
	return pages
}

func readEvidence(path, videoID, generation string) map[string]knowledgeprojection.EvidenceReferenceProjection {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open evidence: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		fatalf("evidence file is empty")
	}
	result := make(map[string]knowledgeprojection.EvidenceReferenceProjection)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 4 {
			fatalf("evidence row has fewer than 4 columns")
		}
		start, err1 := strconv.Atoi(fields[2])
		end, err2 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil {
			fatalf("invalid evidence time range")
		}
		result[fields[0]] = knowledgeprojection.EvidenceReferenceProjection{EvidenceSentenceID: fields[0], ChunkRefs: []string{fields[1]}, StartMs: start, EndMs: end, SourceVideoID: videoID, TranscriptGeneration: generation}
	}
	if err := scanner.Err(); err != nil {
		fatalf("read evidence: %v", err)
	}
	return result
}

func cloneDocument(document summary.Document) summary.Document {
	data, _ := json.Marshal(document)
	var clone summary.Document
	_ = json.Unmarshal(data, &clone)
	return clone
}

func writeArtifacts(dir string, page *weknora.WikiPage, draft, candidate summary.Document, before, after knowledgeprojection.SummaryDoubleReferenceResult, bindErr, validationErr error, coverage []knowledgeprojection.SummaryKnowledgeCoverage, scope knowledgeprojection.SummaryReferenceScope, bindings []knowledgeprojection.SummaryKnowledgeBinding, videoID, generation, videoType string) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		fatalf("create output directory: %v", err)
	}
	writeJSON(dir+"/05-summary-before.json", map[string]any{"task_id": "VW-P4-05", "video_id": videoID, "transcript_generation": generation, "video_type": videoType, "source_page_id": page.ID, "source_slug": page.Slug, "source_version": page.Version, "draft": redactDocument(draft), "audit": before})
	writeJSON(dir+"/05-summary-after.json", map[string]any{"task_id": "VW-P4-05", "video_id": videoID, "transcript_generation": generation, "video_type": videoType, "persisted": false, "final_candidate": redactDocument(candidate), "bind_error": errorString(bindErr), "validation_error": errorString(validationErr), "audit": after, "knowledge_binding_count": len(bindings), "knowledge_page_count": len(scope.Pages)})
	file, err := os.Create(dir + "/05-summary-reference-audit.ndjson")
	if err != nil {
		fatalf("create audit: %v", err)
	}
	defer file.Close()
	for _, audit := range append(append([]knowledgeprojection.SummaryReferenceAudit{}, before.DraftAudits...), after.FinalAudits...) {
		data, _ := json.Marshal(audit)
		_, _ = file.Write(append(data, '\n'))
	}
	writeJSON(dir+"/05-summary-fallback-cases.json", []map[string]any{{"case": "unknown_wiki_page", "status": "covered_by_unit_test", "expected": "draft retained; final rejected"}, {"case": "invalid_evidence_anchor", "status": "covered_by_unit_test", "expected": "final rejected"}, {"case": "active_generation_mismatch", "status": "covered_by_unit_test", "expected": "summary audit stops"}, {"case": "unmapped_summary_block", "status": map[string]any{"bind_error": errorString(bindErr), "final_promotable": after.FinalPromotable}, "expected": "draft retained; final not promoted"}})
	writeJSON(dir+"/05-summary-coverage-gap.json", coverage)
	completeBlocks, missingEvidenceRefs := 0, 0
	for _, item := range coverage {
		if item.Complete {
			completeBlocks++
		}
		missingEvidenceRefs += len(item.MissingEvidenceIDs)
	}
	writeText(dir+"/05-evidence-package.md", fmt.Sprintf("# VW-P4-05 总结证据包\n\n- 视频 ID：`%s`\n- 转写代次：`%s`\n- 总结类型：`%s`\n- 当前总结页：`%s`（版本 %d）\n- 知识对象页：%d\n- 当前代次证据引用：%d\n- 草稿区块审计：%d\n- 总结区块逐条覆盖：%d/%d\n- 缺失证据引用：%d\n- 最终候选可提升：`%t`\n- 失败降级：`%t`\n- 本次未写入 Wiki，未写入 Graph，未部署，未重启。\n", videoID, generation, videoType, page.ID, page.Version, len(scope.Pages), len(scope.Evidence), len(before.DraftAudits), completeBlocks, len(coverage), missingEvidenceRefs, after.FinalPromotable, after.FallbackRequired))
}

func redactDocument(document summary.Document) map[string]any {
	sections := make([]map[string]any, 0, len(document.Sections))
	for _, section := range document.Sections {
		blocks := make([]map[string]any, 0, len(section.Blocks))
		for _, block := range section.Blocks {
			blocks = append(blocks, map[string]any{"id": block.ID, "evidence_chunk_count": len(block.EvidenceChunkIDs), "evidence_ref_count": len(block.EvidenceRefs), "knowledge_ref_ids": block.KnowledgeRefs})
		}
		sections = append(sections, map[string]any{"id": section.ID, "block_count": len(section.Blocks), "blocks": blocks})
	}
	return map[string]any{"schema_version": document.SchemaVersion, "video_type": document.VideoType, "section_count": len(document.Sections), "sections": sections}
}
func countAudits(audits []knowledgeprojection.SummaryReferenceAudit) int { return len(audits) }
func stringValue(value any) string                                       { text, _ := value.(string); return text }
func stringSlice(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
	case []string:
		result = append(result, typed...)
	}
	return result
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func firstError(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
func writeJSON(path string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		fatalf("write %s: %v", path, err)
	}
}
func writeText(path, value string) {
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		fatalf("write %s: %v", path, err)
	}
}
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vw-p4-05: "+format+"\n", args...)
	os.Exit(1)
}
