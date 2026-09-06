package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

type inputFile struct {
	Candidates []knowledge.Candidate `json:"candidates"`
}

type fieldFile struct {
	Mappings []struct {
		CandidateID   string              `json:"candidate_id"`
		FieldEvidence map[string][]string `json:"field_evidence"`
	} `json:"mappings"`
}

func main() {
	inputPath := flag.String("input", "", "candidate JSON")
	evidencePath := flag.String("evidence", "", "newline-delimited evidence IDs")
	fieldPath := flag.String("field-evidence", "", "field evidence JSON")
	reportPath := flag.String("report", "", "output report JSON")
	flag.Parse()
	if *inputPath == "" || *evidencePath == "" || *fieldPath == "" || *reportPath == "" {
		panic("input, evidence, field-evidence and report are required")
	}
	var in inputFile
	readJSON(*inputPath, &in)
	var fields fieldFile
	readJSON(*fieldPath, &fields)
	evidenceRaw, err := os.ReadFile(*evidencePath)
	if err != nil {
		panic(err)
	}
	var evidenceIDs []string
	for _, line := range strings.Split(string(evidenceRaw), "\n") {
		if strings.TrimSpace(line) != "" {
			evidenceIDs = append(evidenceIDs, strings.TrimSpace(line))
		}
	}
	ctx := knowledge.DocumentContext{SourceDocumentID: "c611c62a-2dc7-4471-b73b-ff31cc24b9e7", SourceVideoID: "ee6a5bea-26f8-4aa0-87c9-c0a64d9b9024", TranscriptGeneration: "d2c8487042a1a820f7c98e69dc5812ac6ea836b563996e435c478d213c51444b", Summary: "当前视频整篇源文档候选适配上下文", EvidenceIDs: evidenceIDs}
	if err := ctx.Validate(); err != nil {
		panic(err)
	}
	fieldByID := make(map[string]map[string][]string, len(fields.Mappings))
	for _, m := range fields.Mappings {
		fieldByID[m.CandidateID] = m.FieldEvidence
	}
	results := make([]knowledge.ClassifiedKnowledge, 0, len(in.Candidates))
	rejections := make([]map[string]string, 0)
	for _, candidate := range in.Candidates {
		classified, err := knowledge.Classify(candidate, ctx)
		if err != nil {
			rejections = append(rejections, map[string]string{"candidate_id": candidate.ID, "reason": err.Error()})
			continue
		}
		if _, ok := fieldByID[candidate.ID]; !ok {
			rejections = append(rejections, map[string]string{"candidate_id": candidate.ID, "reason": "field evidence mapping missing"})
			continue
		}
		results = append(results, classified)
	}
	gate := knowledge.ApplyPublishGate(results)
	passed := make([]string, 0, len(gate.Passed))
	for _, object := range gate.Passed {
		passed = append(passed, object.CandidateID)
	}
	sort.Strings(passed)
	report := map[string]any{"task_id": "VW-P3-GAP-15", "status": "passed_with_candidates", "candidate_count": len(in.Candidates), "classified_count": len(results), "publish_passed_count": len(gate.Passed), "publish_rejected_count": len(gate.Rejected), "passed_candidate_ids": passed, "rejections": rejections, "write_invoked": false}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*reportPath, append(out, '\n'), 0600); err != nil {
		panic(err)
	}
	fmt.Println(string(out))
}

func readJSON(path string, target any) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		panic(err)
	}
}
