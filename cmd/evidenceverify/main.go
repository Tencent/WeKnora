// Command evidenceverify validates the deterministic checksum of an exported
// evaluation evidence report. The checksum proves integrity, not authorship.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Tencent/WeKnora/internal/types"
)

func main() {
	path := flag.String("report", "", "evaluation evidence JSON file")
	flag.Parse()
	if *path == "" {
		fatal(errors.New("-report is required"))
	}
	file, err := os.Open(*path)
	if err != nil {
		fatal(err)
	}
	defer file.Close()
	if err := verify(file); err != nil {
		fatal(err)
	}
	fmt.Println("evaluation evidence checksum verified")
}

func verify(reader io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(reader, 64<<20))
	if err != nil {
		return err
	}
	var kind struct {
		BenchmarkID string `json:"benchmark_id"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return fmt.Errorf("parse report: %w", err)
	}
	if kind.BenchmarkID != "" {
		var report types.WikiCacheBenchmarkEvidence
		if err := json.Unmarshal(data, &report); err != nil {
			return fmt.Errorf("parse report: %w", err)
		}
		return verifyWikiCacheReport(&report)
	}
	var report types.EvaluationEvidenceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("parse report: %w", err)
	}
	return verifyEvaluationReport(&report)
}

func verifyEvaluationReport(report *types.EvaluationEvidenceReport) error {
	if report.ReportSHA256 == "" {
		return errors.New("report_sha256 is empty")
	}
	want := report.ReportSHA256
	report.ReportSHA256 = ""
	canonical, err := json.Marshal(&report)
	if err != nil {
		return fmt.Errorf("canonicalize report: %w", err)
	}
	got := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func verifyWikiCacheReport(report *types.WikiCacheBenchmarkEvidence) error {
	if report.ReportSHA256 == "" {
		return errors.New("report_sha256 is empty")
	}
	want := report.ReportSHA256
	report.ReportSHA256 = ""
	canonical, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("canonicalize report: %w", err)
	}
	got := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
