package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgeprojection"
)

func main() {
	var baseURL, apiKey, tenantID, kbID, videoID, generation, pagesPath, relationsPath, reportPath string
	var durationMs int64
	flag.StringVar(&baseURL, "base-url", "", "WeKnora base URL")
	flag.StringVar(&apiKey, "api-key", os.Getenv("P4_WEKNORA_API_KEY"), "WeKnora API key")
	flag.StringVar(&tenantID, "tenant-id", "10000", "WeKnora tenant ID")
	flag.StringVar(&kbID, "kb-id", "", "isolated Wiki knowledge base")
	flag.StringVar(&videoID, "video-id", "", "active video ID")
	flag.StringVar(&generation, "generation", "", "active transcript generation")
	flag.StringVar(&pagesPath, "pages", "", "P4-03 object page IDs TSV")
	flag.StringVar(&relationsPath, "relations", "", "P4-04 relation input JSON")
	flag.StringVar(&reportPath, "report", "", "P4-04 projection report JSON")
	flag.Int64Var(&durationMs, "duration-ms", 0, "active video duration in milliseconds")
	flag.Parse()
	for name, value := range map[string]string{"base-url": baseURL, "api-key": apiKey, "kb-id": kbID, "video-id": videoID, "generation": generation, "pages": pagesPath, "relations": relationsPath, "report": reportPath} {
		if strings.TrimSpace(value) == "" {
			fatalf("%s is required", name)
		}
	}
	if durationMs <= 0 {
		fatalf("duration-ms must be greater than zero")
	}
	pages := readPageIDs(pagesPath, videoID, generation)
	var inputs []knowledgeprojection.RelationInput
	decodeJSON(relationsPath, &inputs)
	client := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, TenantID: tenantID, KnowledgeKBID: kbID})
	ctx := context.Background()
	projection := knowledgeprojection.AuditRelations(inputs, pages, videoID, generation, durationMs)
	allPages, err := client.ListAllPages(ctx, kbID, "")
	if err != nil {
		fatalf("list isolated Wiki pages: %v", err)
	}
	before := redactPages(allPages)
	reading, invalid := knowledgeprojection.ProjectReadingAssociations(allPages)
	projection.Reading, projection.InvalidLinks = reading, invalid
	var writes []knowledgeprojection.RelationWriteResult
	if len(projection.Semantic) > 0 {
		writes, err = (knowledgeprojection.RelationPagePublisher{Wiki: client, KBID: kbID}).PublishRelations(ctx, pages, projection)
		if err != nil {
			writeJSON(reportPath, map[string]any{"status": "failed", "error": err.Error(), "projection": projection})
			fatalf("publish relations: %v", err)
		}
	}
	afterPages, err := client.ListAllPages(ctx, kbID, "")
	if err != nil {
		fatalf("list isolated Wiki pages after write: %v", err)
	}
	writeJSON(reportPath, map[string]any{"status": "passed", "projection": projection, "writes": writes, "before": before, "after": redactPages(afterPages)})
	fmt.Printf("relation audit passed: semantic=%d reading=%d invalid_links=%d orphans=%d\n", len(projection.Semantic), len(projection.Reading), len(projection.InvalidLinks), len(projection.Orphans))
}

func redactPages(pages []weknora.WikiPage) []map[string]any {
	result := make([]map[string]any, 0, len(pages))
	for _, page := range pages {
		result = append(result, map[string]any{"wiki_page_id": page.ID, "slug": page.Slug, "version": page.Version, "page_type": page.PageType, "in_link_count": len(page.InLinks), "out_link_count": len(page.OutLinks)})
	}
	return result
}

func readPageIDs(path, videoID, generation string) []knowledgeprojection.ObjectPageResult {
	file, err := os.Open(path)
	if err != nil {
		fatalf("open page ID map: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		fatalf("page ID map is empty")
	}
	pages := make([]knowledgeprojection.ObjectPageResult, 0)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 7 {
			fatalf("page ID map row has fewer than 7 columns")
		}
		version, err := strconv.Atoi(fields[6])
		if err != nil {
			fatalf("invalid page version for %s: %v", fields[0], err)
		}
		pages = append(pages, knowledgeprojection.ObjectPageResult{CandidateID: fields[0], PrimaryType: knowledgeprojectionType(fields[1]), WikiPageID: fields[4], Slug: fields[5], Version: version, SourceVideoID: videoID, TranscriptGeneration: generation})
	}
	if err := scanner.Err(); err != nil {
		fatalf("read page ID map: %v", err)
	}
	return pages
}

func knowledgeprojectionType(value string) knowledge.KnowledgeType {
	return knowledge.KnowledgeType(strings.TrimSpace(value))
}

func decodeJSON(path string, target any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("read JSON input: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		fatalf("decode JSON input: %v", err)
	}
}

func writeJSON(path string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatalf("encode JSON report: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		fatalf("write JSON report: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vw-p4-04: "+format+"\n", args...)
	os.Exit(1)
}
