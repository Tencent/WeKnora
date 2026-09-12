// parser-benchmark evaluates the production document-reader adapters without
// creating knowledge bases, embedding documents, or invoking an LLM judge.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var (
	commitID  = "unknown"
	validID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,180}$`)
	signedURL = regexp.MustCompile(`(https?://[^\s"<>?]+)\?[^\s"<>]+`)
)

type sample struct {
	ID       string `json:"id"`
	PDFPath  string `json:"pdf_path"`
	SHA256   string `json:"sha256"`
	Language string `json:"language"`
}
type manifest struct {
	Samples []sample `json:"samples"`
}
type credentials struct {
	AESKey       string                         `json:"aes_key"`
	Cloud        *types.WeKnoraCloudCredentials `json:"cloud"`
	ParserConfig *types.ParserEngineConfig      `json:"parser_config"`
}
type record struct {
	SchemaVersion  int               `json:"schema_version"`
	Engine         string            `json:"engine"`
	SampleID       string            `json:"sample_id"`
	InputSHA256    string            `json:"input_sha256"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	SourceCommit   string            `json:"source_commit"`
	BinarySHA256   string            `json:"binary_sha256"`
	StartedAt      string            `json:"started_at"`
	Status         string            `json:"status"`
	DurationMS     int64             `json:"duration_ms"`
	MarkdownSHA256 string            `json:"markdown_sha256"`
	MarkdownChars  int               `json:"markdown_chars"`
	ImageCount     int               `json:"image_count"`
	Error          string            `json:"error,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Config         map[string]string `json:"config"`
	FallbackStatus string            `json:"fallback_status"`
	CostUSD        *float64          `json:"cost_usd"`
	CostBasis      string            `json:"cost_basis"`
	OutputScope    string            `json:"output_scope"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".tmp", append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func redact(s string, secretValues []string) string {
	for _, secret := range secretValues {
		if len(secret) > 3 {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return signedURL.ReplaceAllString(s, "$1?[REDACTED]")
}

func publicOverrides(overrides map[string]string, secrets []string) map[string]string {
	public := map[string]string{}
	for k, v := range overrides {
		if strings.Contains(k, "key") || strings.Contains(k, "token") {
			public[k] = "configured"
		} else {
			public[k] = redact(v, secrets)
		}
	}
	return public
}

func validateResume(old, expected record, markdown []byte) error {
	oldConfig, _ := json.Marshal(old.Config)
	wantConfig, _ := json.Marshal(expected.Config)
	if old.Engine != expected.Engine || old.SampleID != expected.SampleID || old.InputSHA256 != expected.InputSHA256 ||
		old.ManifestSHA256 != expected.ManifestSHA256 || old.SourceCommit != expected.SourceCommit ||
		old.BinarySHA256 == "" || old.BinarySHA256 != expected.BinarySHA256 ||
		string(oldConfig) != string(wantConfig) ||
		old.MarkdownSHA256 != hash(markdown) {
		return errors.New("resume identity mismatch; preserve existing evidence and choose a new output directory")
	}
	switch old.Status {
	case "success", "error", "timeout", "empty":
		return nil
	default:
		return errors.New("stored result has an unknown status")
	}
}

func engineOverrides(engine string, c *types.ParserEngineConfig, language string) map[string]string {
	all := c.ToOverridesMap()
	out := map[string]string{}
	for k, v := range all {
		keep := false
		switch engine {
		case "mineru":
			keep = strings.HasPrefix(k, "mineru_") && !strings.HasPrefix(k, "mineru_cloud_") && k != "mineru_api_key"
		case "mineru_cloud":
			keep = strings.HasPrefix(k, "mineru_cloud_") || k == "mineru_api_key"
		case "paddleocr_vl":
			keep = strings.HasPrefix(k, "paddleocr_vl_") && !strings.HasPrefix(k, "paddleocr_vl_cloud_")
		case "paddleocr_vl_cloud":
			keep = strings.HasPrefix(k, "paddleocr_vl_cloud_")
		case "opendataloader":
			keep = strings.HasPrefix(k, "odl_")
		}
		if keep {
			out[k] = v
		}
	}
	if engine == "mineru" {
		out["mineru_model"] = "pipeline"
		out["mineru_parse_method"] = "auto"
	}
	if engine == "opendataloader" {
		out["odl_hybrid"] = "off"
	}
	if strings.HasPrefix(engine, "mineru") {
		lang := "ch"
		if strings.HasPrefix(strings.ToLower(language), "en") {
			lang = "en"
		}
		key := "mineru_language"
		if engine == "mineru_cloud" {
			key = "mineru_cloud_language"
		}
		out[key] = lang
	}
	return out
}

func run() error {
	manifestPath := flag.String("manifest", "", "frozen sample manifest")
	root := flag.String("root", ".", "repository root for relative PDF paths")
	output := flag.String("output", "", "new or resumable output directory")
	engine := flag.String("engine", "", "one of the eight benchmark engines")
	docreaderAddr := flag.String("docreader", "127.0.0.1:50051", "DocReader gRPC address")
	maxSamples := flag.Int("max-samples", 100, "hard cap on submissions; 1 to 100")
	timeout := flag.Duration("timeout", 10*time.Minute, "deadline for each PDF")
	execute := flag.Bool("execute", false, "submit documents to selected engine")
	flag.Parse()
	allowed := map[string]bool{
		"builtin":            true,
		"markitdown":         true,
		"opendataloader":     true,
		"weknoracloud":       true,
		"mineru":             true,
		"mineru_cloud":       true,
		"paddleocr_vl":       true,
		"paddleocr_vl_cloud": true,
	}
	if !allowed[*engine] || *manifestPath == "" || *output == "" || *maxSamples < 1 || *maxSamples > 100 {
		return errors.New("manifest, output, known engine and max-samples 1..100 are required")
	}
	if *timeout <= 0 || *timeout > 30*time.Minute {
		return errors.New("timeout must be positive and at most 30m")
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	var m manifest
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	if len(m.Samples) == 0 || len(m.Samples) > *maxSamples {
		return errors.New("manifest empty or exceeds submission cap")
	}
	seen := map[string]bool{}
	for _, s := range m.Samples {
		if !validID.MatchString(s.ID) || seen[s.ID] {
			return errors.New("invalid or duplicate sample ID")
		}
		seen[s.ID] = true
		path := s.PDFPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(*root, path)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if hash(b) != s.SHA256 || !strings.HasPrefix(string(b[:min(len(b), 5)]), "%PDF-") {
			return fmt.Errorf("PDF identity invalid: %s", s.ID)
		}
	}
	if !*execute {
		return json.NewEncoder(os.Stdout).
			Encode(map[string]any{
				"preflight": "passed", "engine": *engine, "samples": len(m.Samples), "network_calls": 0,
			})
	}
	var creds credentials
	if err = json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&creds); err != nil {
		return errors.New("credential JSON must be provided through stdin")
	}
	if creds.ParserConfig == nil {
		creds.ParserConfig = &types.ParserEngineConfig{}
	}
	secrets := []string{creds.AESKey}
	for _, ptr := range []*string{&creds.ParserConfig.MinerUAPIKey, &creds.ParserConfig.PaddleOCRVLCloudToken} {
		secrets = append(secrets, *ptr)
		if strings.HasPrefix(*ptr, utils.EncPrefix) {
			if len(creds.AESKey) != 32 {
				return errors.New("invalid encryption key")
			}
			*ptr, err = utils.DecryptAESGCM(*ptr, []byte(creds.AESKey))
			if err != nil {
				return errors.New("parser credential decryption failed")
			}
		}
		secrets = append(secrets, *ptr)
	}
	if creds.Cloud != nil {
		secrets = append(secrets, creds.Cloud.AppID, creds.Cloud.AppSecret)
		if strings.HasPrefix(creds.Cloud.AppSecret, utils.EncPrefix) {
			if len(creds.AESKey) != 32 {
				return errors.New("invalid encryption key")
			}
			creds.Cloud.AppSecret, err = utils.DecryptAESGCM(creds.Cloud.AppSecret, []byte(creds.AESKey))
			if err != nil {
				return errors.New("cloud credential decryption failed")
			}
		}
		secrets = append(secrets, creds.Cloud.AppSecret)
	}
	// Production adapters sometimes log provider error bodies. The benchmark
	// persists only redacted error text and never emits adapter logs or keys.
	logger.SetOutput(io.Discard)
	remote, err := docparser.NewGRPCDocumentReader(*docreaderAddr)
	if err != nil {
		return err
	}
	defer func() { _ = remote.Close() }()
	dir := filepath.Join(*output, *engine)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	manifestHash := hash(data)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	binaryHash := hash(binary)
	failures := 0
	for _, s := range m.Samples {
		resultPath := filepath.Join(dir, s.ID+".json")
		overrides := engineOverrides(*engine, creds.ParserConfig, s.Language)
		publicConfig := publicOverrides(overrides, secrets)
		r := record{
			SchemaVersion:  2,
			Engine:         *engine,
			SampleID:       s.ID,
			InputSHA256:    s.SHA256,
			ManifestSHA256: manifestHash,
			SourceCommit:   commitID,
			BinarySHA256:   binaryHash,
			StartedAt:      time.Now().UTC().Format(time.RFC3339),
			Config:         publicConfig,
			FallbackStatus: "not_reported_by_adapter",
			CostBasis:      "provider_invoice_not_available",
			OutputScope:    "production_reader_adapter_markdown_before_chunking_and_image_OCR",
		}
		if old, e := os.ReadFile(resultPath); e == nil {
			var saved record
			if json.Unmarshal(old, &saved) != nil {
				return errors.New("stored result is not valid JSON")
			}
			md, e := os.ReadFile(filepath.Join(dir, s.ID+".md"))
			if e != nil {
				return errors.New("stored Markdown is missing; preserve evidence and use a new output directory")
			}
			if e = validateResume(saved, r, md); e != nil {
				return e
			}
			if saved.Status != "success" {
				failures++
			}
			fmt.Printf("resume engine=%s sample=%s status=%s\n", *engine, s.ID, saved.Status)
			continue
		}
		path := s.PDFPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(*root, path)
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if *engine == "builtin" || *engine == "markitdown" || *engine == "opendataloader" || *engine == "mineru" ||
			*engine == "paddleocr_vl" {
			zero := 0.0
			r.CostUSD = &zero
			r.CostBasis = "local_execution_no_API_fee_excludes_hardware_and_energy"
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start := time.Now()
		reader, e := docparser.NewReader(
			ctx,
			*engine,
			"pdf",
			false,
			docparser.ReaderDeps{
				Overrides:               overrides,
				Remote:                  remote,
				WeKnoraCloudCredentials: func(context.Context) *types.WeKnoraCloudCredentials { return creds.Cloud },
			},
		)
		var result *types.ReadResult
		if e == nil {
			result, e = reader.Read(
				ctx,
				&types.ReadRequest{
					FileContent:           content,
					FileName:              s.ID + ".pdf",
					FileType:              "pdf",
					ParserEngine:          *engine,
					RequestID:             "parser-benchmark-" + s.ID,
					ParserEngineOverrides: overrides,
				},
			)
		}
		r.DurationMS = time.Since(start).Milliseconds()
		r.Status = "success"
		if e != nil {
			r.Status = "error"
			if ctx.Err() != nil {
				r.Status = "timeout"
			}
			r.Error = redact(e.Error(), secrets)
		}
		cancel()
		md := ""
		if result != nil {
			md = result.MarkdownContent
			r.ImageCount = len(result.ImageRefs)
			r.Metadata = map[string]string{}
			for k, v := range result.Metadata {
				r.Metadata[k] = redact(v, secrets)
			}
			if result.Error != "" {
				r.Status = "error"
				r.Error = redact(result.Error, secrets)
			}
			if fallback := result.Metadata["parser_fallback"]; fallback != "" {
				r.FallbackStatus = redact(fallback, secrets)
			}
		}
		if r.Status == "success" && strings.TrimSpace(md) == "" {
			r.Status = "empty"
		}
		r.MarkdownSHA256 = hash([]byte(md))
		r.MarkdownChars = utf8.RuneCountInString(md)
		if err = os.WriteFile(filepath.Join(dir, s.ID+".md"), []byte(md), 0o644); err != nil {
			return err
		}
		if err = writeJSON(resultPath, r); err != nil {
			return err
		}
		if r.Status != "success" {
			failures++
		}
		fmt.Printf(
			"engine=%s sample=%s status=%s chars=%d duration_ms=%d\n",
			*engine,
			s.ID,
			r.Status,
			r.MarkdownChars,
			r.DurationMS,
		)
		// Authorization errors are not transient; do not submit the remaining PDFs.
		lower := strings.ToLower(r.Error)
		if strings.Contains(lower, "401") || strings.Contains(lower, "403") ||
			strings.Contains(lower, "insufficient") ||
			strings.Contains(lower, "quota") {
			return fmt.Errorf("engine unavailable; remaining submissions stopped: %s", r.Error)
		}
	}
	return json.NewEncoder(os.Stdout).
		Encode(map[string]any{"engine": *engine, "samples": len(m.Samples), "non_success": failures})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
