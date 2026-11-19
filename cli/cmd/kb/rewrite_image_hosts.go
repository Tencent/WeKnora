package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// Image URL host rewrite statuses. A document ends in exactly one of them.
const (
	imageHostStatusDone         = "done"
	imageHostStatusWouldUpdate  = "would_update"
	imageHostStatusNoMatch      = "no_match"
	imageHostStatusChunkOnly    = "chunk_only"
	imageHostStatusNonManual    = "skipped_non_manual"
	imageHostStatusNoContent    = "skipped_no_content"
	imageHostStatusFailed       = "failed"
	imageHostStatusUnverified   = "unverified"
	imageHostPollInterval       = 2 * time.Second
	imageHostChunkPageSize      = 100
	imageHostDefaultTimeout     = 5 * time.Minute
	imageHostDefaultConcurrency = 4
	imageHostDefaultPageSize    = 100
	imageHostMaxConcurrency     = 32
)

// imageHostChunkTypes covers every chunk shape that can carry a source URL.
// Text-only requests are the server default, so the tool asks explicitly.
var imageHostChunkTypes = []string{
	"text", "parent_text", "image_ocr", "image_caption", "image_vector",
	"summary", "table_summary", "table_column", "faq", "wiki_page",
}

var imageHostImageChunkTypes = []string{"image_ocr", "image_caption", "image_vector"}

var imageHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

var imageReferencePattern = regexp.MustCompile(`!\[[^\]]*\]\(|<img\b`)

// RewriteImageHostsOptions captures `kb rewrite-image-hosts` flag state.
type RewriteImageHostsOptions struct {
	From        string
	To          string
	Apply       bool
	DryRun      bool
	Limit       int
	Concurrency int
	Resume      string
	Timeout     time.Duration
	Verbose     bool
	PageSize    int
	Yes         bool
}

// ImageHostRewriteService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type ImageHostRewriteService interface {
	ListKnowledgeWithFilter(ctx context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error)
	GetKnowledge(ctx context.Context, id string) (*sdk.Knowledge, error)
	ListKnowledgeChunks(ctx context.Context, knowledgeID string, page, pageSize int, chunkTypes ...string) ([]sdk.Chunk, int64, error)
	UpdateManualKnowledge(ctx context.Context, knowledgeID string, request *sdk.UpdateManualKnowledgeRequest) (*sdk.Knowledge, error)
}

// imageHostDocResult is the per-document report and the resume-state entry.
type imageHostDocResult struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status"`
	ContentURLs int    `json:"content_urls"`
	ChunkURLs   int    `json:"chunk_urls"`
	Replaced    int    `json:"replaced"`
	Reason      string `json:"reason,omitempty"`
}

// imageHostSummary is the command envelope's data object.
type imageHostSummary struct {
	KB               string               `json:"kb"`
	From             string               `json:"from"`
	To               string               `json:"to"`
	Mode             string               `json:"mode"`
	DryRun           bool                 `json:"dry_run"`
	Scanned          int                  `json:"scanned"`
	Matched          int                  `json:"matched_documents"`
	ContentURLs      int                  `json:"content_urls"`
	ChunkURLs        int                  `json:"chunk_urls"`
	Replaced         int                  `json:"replaced"`
	WouldUpdate      int                  `json:"would_update"`
	Updated          int                  `json:"updated"`
	Unverified       int                  `json:"unverified"`
	Failed           int                  `json:"failed"`
	NoMatch          int                  `json:"no_match"`
	ChunkOnly        int                  `json:"chunk_only"`
	SkippedNonManual int                  `json:"skipped_non_manual"`
	SkippedNoContent int                  `json:"skipped_no_content"`
	ResumedSkipped   int                  `json:"resumed_skipped"`
	StateError       string               `json:"state_error,omitempty"`
	DurationMS       int64                `json:"duration_ms"`
	Documents        []imageHostDocResult `json:"documents,omitempty"`
}

// imageHostState is the --resume file.
type imageHostState struct {
	KB        string                        `json:"kb"`
	From      string                        `json:"from"`
	To        string                        `json:"to"`
	UpdatedAt string                        `json:"updated_at,omitempty"`
	Documents map[string]imageHostDocResult `json:"documents"`
}

// NewCmdRewriteImageHosts builds `weknora kb rewrite-image-hosts`.
func NewCmdRewriteImageHosts(f *cmdutil.Factory) *cobra.Command {
	opts := &RewriteImageHostsOptions{}
	cmd := &cobra.Command{
		Use:   "rewrite-image-hosts",
		Short: "Rewrite image URL hosts in manual knowledge content",
		Long: `Scans every document of a knowledge base, counts image URLs whose host is
--from, and rewrites only the host to --to. Markdown images, HTML <img> tags
and bare links are covered; every other byte of the document is preserved.

The command is read-only unless --apply is passed. --apply updates manual
knowledge through PUT /api/v1/knowledge/manual/{id}, preserving title, tags,
custom_metadata and parent_knowledge_id, then waits until the reprocessed
image_ocr / image_caption / image_vector chunks reach index_status=ready (or
--timeout). A document still containing --from is the only one rewritten, so
re-running is a no-op.

Failures are recorded per document and never abort the batch. --resume records
doc id -> status so a long run can continue without re-reading finished docs.
Only manual knowledge has an editable body; other types are reported as
skipped_non_manual (their chunk hits are still counted).`,
		Example: `  weknora kb rewrite-image-hosts --kb 57c12fad-6063-44fe-ad22-9f0ec83ded0d \
    --from img2.tapimg.com --to img2-tc.tapimg.com
  weknora kb rewrite-image-hosts --kb my-kb --from img2.tapimg.com \
    --to img2-tc.tapimg.com --apply --limit 50 --concurrency 4 \
    --resume /tmp/rewrite-state.json --verbose -y`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			if err := validateRewriteImageHostsOptions(opts); err != nil {
				return err
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			svc, err := f.Client()
			if err != nil {
				return err
			}
			if opts.Apply {
				retry := cmdutil.BuildRetryArgv(c,
					[]string{"weknora", "kb", "rewrite-image-hosts"},
					"kb", "from", "to", "apply", "limit", "concurrency", "resume", "timeout", "verbose", "format")
				if err := cmdutil.ConfirmWrite(f.Prompter(), opts.Yes, fopts.WantsJSON(),
					"rewrite image hosts in", "knowledge documents", kbID,
					"kb.rewrite_image_hosts", retry); err != nil {
					return err
				}
			}
			return runRewriteImageHosts(c.Context(), opts, fopts, svc, kbID)
		},
	}
	cmdutil.AddKBFlag(cmd)
	cmd.Flags().StringVar(&opts.From, "from", "", "Image host to replace (bare host, e.g. img2.tapimg.com)")
	cmd.Flags().StringVar(&opts.To, "to", "", "Replacement image host (bare host, e.g. img2-tc.tapimg.com)")
	cmd.Flags().BoolVar(&opts.Apply, "apply", false, "Write rewritten content back; without it the command only reports")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Report matches without writing (default when --apply is absent)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "Maximum documents to examine (0 = every document)")
	cmd.Flags().IntVar(&opts.Concurrency, "concurrency", imageHostDefaultConcurrency, "Documents processed in parallel (1..32)")
	cmd.Flags().StringVar(&opts.Resume, "resume", "", "State file for resumable runs (records doc id -> status)")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", imageHostDefaultTimeout, "Per-document reprocessing wait; 0 disables waiting")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Print one line per document")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", imageHostDefaultPageSize, "Knowledge list page size (1..1000)")
	cmdutil.AddFormatFlag(cmd, "kb", "from", "to", "mode", "scanned", "matched_documents",
		"content_urls", "chunk_urls", "replaced", "updated", "failed", "documents")
	cmdutil.SetWriteRisk(cmd, "kb.rewrite_image_hosts")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "rewrite only the image URL host inside manual knowledge Markdown; read-only unless --apply",
		RequiredFlags: []string{
			"--from <host> and --to <host> (bare hosts, no scheme)",
		},
		Examples: []string{
			"weknora kb rewrite-image-hosts --kb <kb-id> --from img2.tapimg.com --to img2-tc.tapimg.com --format json",
			"weknora kb rewrite-image-hosts --kb <kb-id> --from img2.tapimg.com --to img2-tc.tapimg.com --apply --limit 50 --resume /tmp/state.json -y --format json",
		},
		Output: "envelope.data is a summary {kb, from, to, mode, scanned, matched_documents, content_urls, chunk_urls, replaced, updated, failed, documents}; documents[] carries per-doc id/status/content_urls/chunk_urls/replaced/reason",
		Warnings: []string{
			"--apply edits published manual knowledge and re-indexes it; without -y the command exits 10.",
			"Only manual knowledge is editable. Non-manual chunk hits are reported but never written.",
			"--apply changes the URL host only; HEIC/JPEG re-encoding can increase image bytes.",
		},
	})
	return cmd
}

func validateRewriteImageHostsOptions(opts *RewriteImageHostsOptions) error {
	from, err := normalizeImageHost(opts.From, "--from")
	if err != nil {
		return err
	}
	to, err := normalizeImageHost(opts.To, "--to")
	if err != nil {
		return err
	}
	if strings.EqualFold(from, to) {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--from and --to must be different hosts",
		}
	}
	opts.From, opts.To = from, to
	if opts.Apply && opts.DryRun {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--apply and --dry-run are mutually exclusive",
			Hint:    "drop one of the flags; omitting --apply already means dry-run",
		}
	}
	if opts.Limit < 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be >= 0, got %d", opts.Limit),
		}
	}
	if opts.Concurrency < 1 || opts.Concurrency > imageHostMaxConcurrency {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--concurrency must be in 1..%d, got %d", imageHostMaxConcurrency, opts.Concurrency),
		}
	}
	if opts.PageSize < 1 || opts.PageSize > 1000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page-size must be in 1..1000, got %d", opts.PageSize),
		}
	}
	if opts.Timeout < 0 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--timeout must be >= 0, got %s", opts.Timeout),
		}
	}
	return nil
}

func normalizeImageHost(value, flag string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", &cmdutil.Error{
			Code:    cmdutil.CodeInputMissingFlag,
			Message: flag + " is required",
			Hint:    "pass a bare host such as img2.tapimg.com",
		}
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/:@ ") {
		return "", &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("%s must be a bare host, got %q", flag, host),
			Hint:    "drop the scheme, port and path, e.g. img2.tapimg.com",
		}
	}
	if !imageHostPattern.MatchString(host) {
		return "", &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("%s is not a valid host: %q", flag, host),
		}
	}
	return host, nil
}

// imageHostURLPattern matches the exact host followed by a URL boundary, so
// img2.tapimg.com.example does not match. The boundary is preserved verbatim;
// only the scheme+host prefix is replaced.
func imageHostURLPattern(host string) *regexp.Regexp {
	return regexp.MustCompile("(?i)(https://" + regexp.QuoteMeta(host) + ")([/?#\t\n\r \"'<>()\\[\\],;]|$)")
}

// countImageHostURLs reports how many times https://host appears in text.
func countImageHostURLs(text, host string) int {
	if text == "" || host == "" {
		return 0
	}
	return len(imageHostURLPattern(host).FindAllStringIndex(text, -1))
}

// rewriteImageHostURLs replaces only the host part of every https://host URL.
// It returns the rewritten text and the number of replacements.
func rewriteImageHostURLs(text, from, to string) (string, int) {
	if text == "" || from == "" {
		return text, 0
	}
	prefix := "https://" + from
	re := imageHostURLPattern(from)
	replaced := 0
	out := re.ReplaceAllStringFunc(text, func(match string) string {
		replaced++
		if len(match) > len(prefix) {
			return "https://" + to + match[len(prefix):]
		}
		return "https://" + to
	})
	return out, replaced
}

func hasImageReference(content string) bool {
	return imageReferencePattern.MatchString(content)
}

func runRewriteImageHosts(
	ctx context.Context, opts *RewriteImageHostsOptions, fopts *cmdutil.FormatOptions,
	svc ImageHostRewriteService, kbID string,
) error {
	state, err := loadImageHostState(opts.Resume, kbID, opts.From, opts.To)
	if err != nil {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: err.Error(),
			Hint:    "use a state file written for the same knowledge base and hosts",
		}
	}
	start := time.Now()
	runner := &imageHostRunner{svc: svc, opts: opts, state: state, now: time.Now}
	summary, runErr := runner.run(ctx, kbID)
	summary.DurationMS = time.Since(start).Milliseconds()
	if runErr != nil {
		return cmdutil.WrapHTTP(runErr, "list knowledge in %s", kbID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, summary, nil)
	}
	printImageHostSummary(iostreams.IO.Out, summary, opts.Verbose)
	return nil
}

func printImageHostSummary(out io.Writer, summary imageHostSummary, verbose bool) {
	mode := "dry-run"
	if !summary.DryRun {
		mode = "apply"
	}
	if verbose {
		for _, doc := range summary.Documents {
			reason := ""
			if doc.Reason != "" {
				reason = "  reason=" + doc.Reason
			}
			fmt.Fprintf(out, "%-36s  %-20s content=%d chunk=%d replaced=%d%s\n",
				doc.ID, doc.Status, doc.ContentURLs, doc.ChunkURLs, doc.Replaced, reason)
		}
	}
	fmt.Fprintf(out, "kb=%s  %s -> %s  mode=%s  scanned=%d  matched=%d  urls=%d (chunk=%d)\n",
		summary.KB, summary.From, summary.To, mode, summary.Scanned, summary.Matched,
		summary.ContentURLs, summary.ChunkURLs)
	fmt.Fprintf(out, "updated=%d  would_update=%d  unverified=%d  failed=%d  no_match=%d  chunk_only=%d  skipped=%d/%d  resumed_skipped=%d  elapsed=%dms\n",
		summary.Updated, summary.WouldUpdate, summary.Unverified, summary.Failed, summary.NoMatch,
		summary.ChunkOnly, summary.SkippedNonManual, summary.SkippedNoContent,
		summary.ResumedSkipped, summary.DurationMS)
	if summary.StateError != "" {
		fmt.Fprintf(out, "state file error: %s\n", summary.StateError)
	}
}

type imageHostRunner struct {
	svc  ImageHostRewriteService
	opts *RewriteImageHostsOptions
	now  func() time.Time

	mu          sync.Mutex
	state       *imageHostState
	results     []imageHostDocResult
	stateErr    string
	resumedSkip int
}

func (r *imageHostRunner) run(ctx context.Context, kbID string) (imageHostSummary, error) {
	jobs := make(chan sdk.Knowledge)
	var wg sync.WaitGroup
	for i := 0; i < r.opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for doc := range jobs {
				r.record(r.processDoc(ctx, doc))
			}
		}()
	}

	var listErr error
	examined := 0
	page := 1
	for {
		rows, total, err := r.svc.ListKnowledgeWithFilter(ctx, kbID, page, r.opts.PageSize, sdk.KnowledgeListFilter{})
		if err != nil {
			listErr = err
			break
		}
		for _, row := range rows {
			if r.skipResumed(row.ID) {
				r.resumedSkip++
				continue
			}
			if r.opts.Limit > 0 && examined >= r.opts.Limit {
				break
			}
			jobs <- row
			examined++
		}
		if r.opts.Limit > 0 && examined >= r.opts.Limit {
			break
		}
		if len(rows) == 0 || int64(page*r.opts.PageSize) >= total {
			break
		}
		page++
	}
	close(jobs)
	wg.Wait()
	if listErr != nil {
		return imageHostSummary{}, listErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	sort.SliceStable(r.results, func(i, j int) bool { return r.results[i].ID < r.results[j].ID })
	summary := imageHostSummary{
		KB: kbID, From: r.opts.From, To: r.opts.To,
		DryRun: !r.opts.Apply, Mode: "dry-run",
		ResumedSkipped: r.resumedSkip, StateError: r.stateErr,
		Documents: append([]imageHostDocResult(nil), r.results...),
	}
	if r.opts.Apply {
		summary.Mode = "apply"
	}
	for _, doc := range r.results {
		summary.Scanned++
		summary.ContentURLs += doc.ContentURLs
		summary.ChunkURLs += doc.ChunkURLs
		summary.Replaced += doc.Replaced
		if doc.ContentURLs > 0 {
			summary.Matched++
		}
		switch doc.Status {
		case imageHostStatusDone:
			summary.Updated++
		case imageHostStatusWouldUpdate:
			summary.WouldUpdate++
		case imageHostStatusUnverified:
			summary.Unverified++
		case imageHostStatusFailed:
			summary.Failed++
		case imageHostStatusNoMatch:
			summary.NoMatch++
		case imageHostStatusChunkOnly:
			summary.ChunkOnly++
		case imageHostStatusNonManual:
			summary.SkippedNonManual++
		case imageHostStatusNoContent:
			summary.SkippedNoContent++
		}
	}
	return summary, nil
}

func (r *imageHostRunner) record(result imageHostDocResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, result)
	r.state.Documents[result.ID] = result
	r.state.UpdatedAt = r.now().UTC().Format(time.RFC3339)
	if r.opts.Resume == "" {
		return
	}
	if err := saveImageHostState(r.opts.Resume, r.state); err != nil && r.stateErr == "" {
		r.stateErr = err.Error()
	}
}

func (r *imageHostRunner) skipResumed(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	doc, ok := r.state.Documents[id]
	if !ok {
		return false
	}
	switch doc.Status {
	case imageHostStatusDone, imageHostStatusNoMatch, imageHostStatusChunkOnly, imageHostStatusNonManual:
		return true
	default:
		return false
	}
}

func (r *imageHostRunner) processDoc(ctx context.Context, doc sdk.Knowledge) imageHostDocResult {
	result := imageHostDocResult{ID: doc.ID, Title: doc.Title, Type: doc.Type}
	detail, err := r.svc.GetKnowledge(ctx, doc.ID)
	if err != nil {
		result.Status = imageHostStatusFailed
		result.Reason = "get: " + err.Error()
		return result
	}
	result.Title = detail.Title
	result.Type = detail.Type
	content, err := detail.ManualContent()
	if err != nil {
		result.Status = imageHostStatusFailed
		result.Reason = "read manual content: " + err.Error()
		return result
	}
	result.ContentURLs = countImageHostURLs(content, r.opts.From)
	if chunkURLs, err := r.scanChunks(ctx, doc.ID); err != nil {
		result.Reason = "chunk scan: " + err.Error()
	} else {
		result.ChunkURLs = chunkURLs
	}
	if !strings.EqualFold(result.Type, "manual") {
		result.Status = imageHostStatusNonManual
		return result
	}
	if strings.TrimSpace(content) == "" {
		result.Status = imageHostStatusNoContent
		return result
	}
	if result.ContentURLs == 0 {
		if result.ChunkURLs > 0 {
			result.Status = imageHostStatusChunkOnly
		} else {
			result.Status = imageHostStatusNoMatch
		}
		return result
	}
	if !r.opts.Apply {
		result.Status = imageHostStatusWouldUpdate
		return result
	}

	rewritten, replaced := rewriteImageHostURLs(content, r.opts.From, r.opts.To)
	if replaced != result.ContentURLs {
		result.Status = imageHostStatusFailed
		result.Reason = fmt.Sprintf("rewrite counted %d URLs but replaced %d", result.ContentURLs, replaced)
		return result
	}
	status := "publish"
	if strings.EqualFold(detail.ParseStatus, "draft") {
		status = "draft"
	}
	if _, err := r.svc.UpdateManualKnowledge(ctx, doc.ID, &sdk.UpdateManualKnowledgeRequest{
		Title:   detail.Title,
		Content: rewritten,
		Status:  status,
	}); err != nil {
		result.Status = imageHostStatusFailed
		result.Reason = "update: " + err.Error()
		return result
	}
	result.Replaced = replaced
	final, err := r.waitForReprocess(ctx, doc.ID, hasImageReference(rewritten))
	if err != nil {
		result.Status = imageHostStatusUnverified
		result.Reason = err.Error()
		return result
	}
	if final != nil && !sameKnowledgeIdentity(detail, final) {
		result.Status = imageHostStatusFailed
		result.Reason = "post-update verification: title / custom_metadata / parent_knowledge_id changed"
		return result
	}
	if final != nil && hasImageReference(rewritten) {
		// CleanMarkdown may normalize whitespace, so the source body is not a
		// byte-stable oracle. The rebuilt chunks are: they must no longer carry
		// the old host and must carry the new one.
		if err := r.verifyChunkHosts(ctx, doc.ID, r.opts.From, r.opts.To); err != nil {
			result.Status = imageHostStatusFailed
			result.Reason = "post-update chunk verification: " + err.Error()
			return result
		}
	}
	result.Status = imageHostStatusDone
	return result
}

// sameKnowledgeIdentity checks the fields the rewrite promises not to touch.
// custom_metadata is compared canonically so key order and a null/{} round-trip
// do not look like a change.
func sameKnowledgeIdentity(before, after *sdk.Knowledge) bool {
	if before == nil || after == nil {
		return before == after
	}
	return before.Title == after.Title &&
		before.ParentKnowledgeID == after.ParentKnowledgeID &&
		reflect.DeepEqual(canonicalJSON(before.CustomMetadata), canonicalJSON(after.CustomMetadata))
}

func canonicalJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	if object, ok := value.(map[string]any); ok && len(object) == 0 {
		return nil
	}
	return value
}

func (r *imageHostRunner) scanChunks(ctx context.Context, knowledgeID string) (int, error) {
	matches := 0
	for page := 1; ; page++ {
		chunks, total, err := r.svc.ListKnowledgeChunks(ctx, knowledgeID, page, imageHostChunkPageSize, imageHostChunkTypes...)
		if err != nil {
			return matches, err
		}
		for _, chunk := range chunks {
			matches += countImageHostURLs(chunk.Content, r.opts.From)
		}
		if len(chunks) == 0 || int64(page*imageHostChunkPageSize) >= total {
			return matches, nil
		}
	}
}

// waitForReprocess blocks until the document is parsed again and its image
// chunks are ready. It returns the latest detail for the identity check, or nil
// when --timeout disabled waiting.
func (r *imageHostRunner) waitForReprocess(
	ctx context.Context, knowledgeID string, expectImages bool,
) (*sdk.Knowledge, error) {
	if r.opts.Timeout <= 0 {
		return nil, nil
	}
	deadline := time.Now().Add(r.opts.Timeout)
	for {
		detail, err := r.svc.GetKnowledge(ctx, knowledgeID)
		if err == nil {
			switch strings.ToLower(detail.ParseStatus) {
			case "failed":
				reason := detail.ErrorMessage
				if reason == "" {
					reason = "parse_status=failed"
				}
				return nil, errors.New("reprocessing failed: " + reason)
			case "completed":
				if !expectImages {
					return detail, nil
				}
				ready, readyErr := r.imageChunksReady(ctx, knowledgeID)
				if readyErr == nil && ready {
					return detail, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for image chunks", r.opts.Timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(imageHostPollInterval):
		}
	}
}

// verifyChunkHosts re-reads every content-bearing chunk after reprocessing and
// requires the old host to be gone and the new host to appear, including the
// image_info JSON of image chunks.
func (r *imageHostRunner) verifyChunkHosts(ctx context.Context, knowledgeID, from, to string) error {
	fromLeft, toSeen := 0, 0
	for page := 1; ; page++ {
		chunks, total, err := r.svc.ListKnowledgeChunks(ctx, knowledgeID, page, imageHostChunkPageSize, imageHostChunkTypes...)
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			for _, text := range []string{chunk.Content, chunk.ImageInfo} {
				fromLeft += countImageHostURLs(text, from)
				toSeen += countImageHostURLs(text, to)
			}
		}
		if len(chunks) == 0 || int64(page*imageHostChunkPageSize) >= total {
			break
		}
	}
	if fromLeft > 0 {
		return fmt.Errorf("%d chunk references still use %s", fromLeft, from)
	}
	if toSeen == 0 {
		return fmt.Errorf("no chunk reference uses %s", to)
	}
	return nil
}

func (r *imageHostRunner) imageChunksReady(ctx context.Context, knowledgeID string) (bool, error) {
	seen := 0
	for page := 1; ; page++ {
		chunks, total, err := r.svc.ListKnowledgeChunks(ctx, knowledgeID, page, imageHostChunkPageSize, imageHostImageChunkTypes...)
		if err != nil {
			return false, err
		}
		for _, chunk := range chunks {
			seen++
			if !strings.EqualFold(chunk.IndexStatus, "ready") {
				return false, nil
			}
		}
		if len(chunks) == 0 || int64(page*imageHostChunkPageSize) >= total {
			break
		}
	}
	return seen > 0, nil
}

func loadImageHostState(path, kbID, from, to string) (*imageHostState, error) {
	state := &imageHostState{
		KB: kbID, From: from, To: to,
		Documents: map[string]imageHostDocResult{},
	}
	if path == "" {
		return state, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var stored imageHostState
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	if stored.KB != "" && stored.KB != kbID {
		return nil, fmt.Errorf("state file belongs to knowledge base %s, not %s", stored.KB, kbID)
	}
	if stored.From != "" && !strings.EqualFold(stored.From, from) {
		return nil, fmt.Errorf("state file was written for --from %s, not %s", stored.From, from)
	}
	if stored.To != "" && !strings.EqualFold(stored.To, to) {
		return nil, fmt.Errorf("state file was written for --to %s, not %s", stored.To, to)
	}
	if stored.Documents == nil {
		stored.Documents = map[string]imageHostDocResult{}
	}
	stored.KB, stored.From, stored.To = kbID, from, to
	return &stored, nil
}

func saveImageHostState(path string, state *imageHostState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
