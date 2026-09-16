package main

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEngineOverridesDoNotSendOtherProvidersCredentials(t *testing.T) {
	c := &types.ParserEngineConfig{
		MinerUAPIKey:          "mineru-secret",
		PaddleOCRVLCloudToken: "paddle-secret",
		MinerUEndpoint:        "http://mineru:8000",
		PaddleOCRVLEndpoint:   "http://paddle:8080",
	}
	for _, engine := range []string{
		"builtin", "markitdown", "opendataloader", "weknoracloud",
		"mineru", "mineru_cloud", "paddleocr_vl", "paddleocr_vl_cloud",
	} {
		got := engineOverrides(engine, c, "en")
		if got["mineru_api_key"] != "" && engine != "mineru_cloud" {
			t.Fatalf("MinerU credential sent to %s", engine)
		}
		if got["paddleocr_vl_cloud_token"] != "" && engine != "paddleocr_vl_cloud" {
			t.Fatalf("Paddle credential sent to %s", engine)
		}
	}
}

func TestRedactSecretsAndSignedDownloads(t *testing.T) {
	got := redact("failure secret-123 https://example.org/result?access_token=abc", []string{"secret-123"})
	if strings.Contains(got, "secret-123") || strings.Contains(got, "access_token=abc") {
		t.Fatal(got)
	}
}

func TestResumeRejectsChangedEvidenceAndConfiguration(t *testing.T) {
	md := []byte("actual result")
	want := record{
		Engine: "mineru", SampleID: "sample-1", InputSHA256: "pdf", ManifestSHA256: "manifest",
		SourceCommit: "build-label", BinarySHA256: "binary-1", MarkdownSHA256: hash(md),
		Status: "success", Config: map[string]string{"model": "pipeline"},
	}
	if err := validateResume(want, want, md); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*record){
		"changed_binary_same_label": func(r *record) { r.BinarySHA256 = "binary-2" },
		"missing_binary_identity":   func(r *record) { r.BinarySHA256 = "" },
		"different_engine":          func(r *record) { r.Engine = "builtin" },
		"different_sample":          func(r *record) { r.SampleID = "sample-2" },
		"different_model":           func(r *record) { r.Config = map[string]string{"model": "vlm"} },
		"unknown_status":            func(r *record) { r.Status = "pending" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			saved := want
			mutate(&saved)
			if validateResume(saved, want, md) == nil {
				t.Fatal("changed evidence reused")
			}
		})
	}
	if validateResume(want, want, []byte("corrupt result")) == nil {
		t.Fatal("corrupt Markdown reused")
	}
}
