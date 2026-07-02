// Package api implements the `weknora api` raw HTTP passthrough command.
//
// Shape: one positional (path) + `-X/--method` flag, default GET (auto-
// promoted to POST when a body is supplied via -d/--input). Text mode writes
// the raw response body to stdout; --format json places the parsed server
// response directly under envelope.data (no {status, headers, body} wrapper),
// so `--jq '.data...'` projects at the server's own depth. Reuses
// sdk.Client.Raw which already applies tenant + auth headers.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// apiFields is intentionally a marker - api wraps arbitrary HTTP responses
// whose schema the CLI doesn't know, so field hints are meaningless here.
// The marker shows up in --help so users can tell.
var apiFields = []string{"<response-shape-varies>"}

type Options struct {
	Method      string
	Input       string   // --input: file path, "-" for stdin
	Data        string   // -d/--data: inline JSON body (mutually exclusive with --input)
	Fields      []string // -F/--field: key=value pairs assembled into a JSON object body (mutually exclusive with -d/--input)
	Yes         bool
	DryRun      bool
	StdinReader io.Reader // overridden by tests; defaults to iostreams.IO.In
}

// Service is the narrow SDK surface this command depends on. The production
// implementation is *sdk.Client, whose Raw method already injects auth /
// tenant / request-id headers (see client.applyAuthHeaders). Tests substitute
// either a fake or a real client pointed at httptest.Server.
type Service interface {
	Raw(ctx context.Context, method, path string, body any) (*http.Response, error)
}

// NewCmd returns the `weknora api` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Make a raw API request to the WeKnora server",
		Long: `Raw HTTP API access. JSON body via -d/--data (inline) or --input <file>/- (stdin).

The default method is GET; supplying a body (-d or --input) auto-promotes it
to POST. Use -X/--method to override (any non-empty method is accepted:
DELETE / PUT / PATCH / HEAD / OPTIONS / TRACE / custom).

Auth, tenant, and request-id headers are applied automatically from the
active profile. In text mode (default) the raw server response body is written
to stdout. In --format json the parsed server response is placed directly
under envelope.data — drill in with --jq '.data...' at the server's own depth
(e.g. '.data.data[]' for a list endpoint). Only -X DELETE is confirmation-gated.

Examples:
  weknora api /api/v1/knowledge-bases                                  # GET
  weknora api /api/v1/knowledge-bases -d '{"name":"foo"}'              # POST (auto)
  weknora api /api/v1/knowledge-bases -F name=foo -F enabled=true      # POST, typed body
  echo '{"name":"foo"}' | weknora api /api/v1/knowledge-bases --input -  # POST via stdin
  weknora api /api/v1/knowledge-bases/kb_xxx -X DELETE`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			if opts.DryRun {
				// --dry-run on the raw escape-hatch is only meaningful when a
				// would-be mutation exists to preview. GET (default or explicit)
				// is read-only with no side effect, so previewing it is a
				// likely user error — signal it via FlagError exit 2 with the
				// concrete repair. Use resolveMethod so --input auto-promotes
				// GET → POST identically to the live path.
				method := resolveMethod(opts)
				if method == http.MethodGet {
					return cmdutil.NewFlagError(fmt.Errorf(
						"--dry-run requires explicit -X POST/PUT/PATCH/DELETE; default GET is read-only with no side effect to preview"))
				}
				var body any
				if hasBody(opts) {
					contents, err := resolveBody(opts)
					if err != nil {
						return err
					}
					// Surface the parsed body in the plan so agents can grep
					// meta.plan.body for shape; resolveBody has already validated
					// it as JSON.
					var parsed any
					if json.Unmarshal(contents, &parsed) == nil {
						body = parsed
					} else {
						body = string(contents)
					}
				}
				if handled, err := cmdutil.HandleDryRun(c, true, cmdutil.DryRunPlan{
					Action: "api." + strings.ToLower(method),
					Method: method,
					Path:   args[0],
					Body:   body,
				}); handled {
					return err
				}
			}
			method := resolveMethod(opts)
			// Escape-hatch DELETE through `weknora api` is just as destructive
			// as `weknora kb delete` - exit-10 destructive protocol must apply
			// (cli/README.md). PUT/PATCH mutate server state like a typed
			// `kb/agent/doc update`, so they get the same exit-10 WRITE gate;
			// without it the raw escape hatch bypassed the "an agent cannot
			// silently mutate" guarantee. POST stays ungated to match typed
			// `create` (also ungated). GET/HEAD are reads.
			switch method {
			case http.MethodDelete:
				if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(), "delete", "endpoint", args[0], "api.delete", []string{"weknora", "api", "-X", "DELETE", args[0], "-y"}); err != nil {
					return err
				}
			case http.MethodPut, http.MethodPatch:
				if err := cmdutil.ConfirmWrite(f.Prompter(), opts.Yes, fopts.WantsJSON(), "write", "endpoint", args[0], "api."+strings.ToLower(method), apiRetryArgv(opts, method, args[0])); err != nil {
					return err
				}
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			paginate, _ := c.Flags().GetBool("paginate")
			return runAPI(c.Context(), opts, fopts, cli, method, args[0], paginate)
		},
	}
	cmd.Flags().StringVarP(&opts.Method, "method", "X", "", "HTTP method (default: GET, or POST when a body is supplied via -d/--input). Any non-empty method is accepted.")
	cmd.Flags().StringVarP(&opts.Data, "data", "d", "", "Inline JSON request body (e.g. -d '{\"name\":\"x\"}'). Mutually exclusive with --input.")
	cmd.Flags().StringVar(&opts.Input, "input", "", "Read JSON request body from file (use `-` for stdin). Mutually exclusive with -d/--data.")
	cmd.Flags().StringArrayVarP(&opts.Fields, "field", "F", nil, "Add a key=value to a JSON object body (repeatable; e.g. -F name=foo -F enabled=true). true/false/null and numbers are typed; everything else is a string. Flat keys only. Mutually exclusive with -d/--input.")
	cmd.Flags().Bool("paginate", false, "Follow offset-based pagination (?page=N&page_size=M), merging all pages into a single {data, total} JSON response.")
	cmdutil.AddFormatFlag(cmd, apiFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "raw HTTP passthrough to weknora-server API endpoints when typed subcommands are insufficient",
		RequiredFlags: []string{"path (positional)"},
		Examples: []string{
			"weknora api /api/v1/knowledge-bases",
			"weknora api /api/v1/knowledge-bases -d '{\"name\":\"foo\"}'",
			"weknora api -X POST /api/v1/knowledge-bases -F name=\"Eng Docs\" -F enabled=true",
			"weknora api -X DELETE /api/v1/knowledge-bases/kb_x -y",
			"echo '{\"name\":\"foo\"}' | weknora api /api/v1/knowledge-bases --input -",
		},
		Output: "text mode (default): the raw server response body on stdout. json mode: the parsed server response is placed directly under envelope.data — project with --jq '.data...' at the server's own depth (e.g. '.data.data[]' for a list endpoint, '.data.data.id' for a created object). With --paginate, envelope.data is the merged {data, total}.",
		Warnings: []string{
			"-X DELETE is destructive-gated and -X PUT/PATCH are write-gated (exit 10 / input.confirmation_required unless -y), matching typed delete/update. -X POST (create-shaped) and GET are unguarded — you own the safety of creates made through this escape hatch.",
			"Raw passthrough: the typed error envelope does NOT fully apply. The server's own response goes under envelope.data at its native depth; a non-2xx HTTP status surfaces via the exit code, not a typed error.type/retry_argv. Do not rely on error.type/retryable for `api` the way you do for typed subcommands.",
			"Raw HTTP passthrough; agents should prefer typed subcommands (kb/doc/session/...) when available.",
		},
	})
	return cmd
}

// readInput reads opts.Input and returns its contents. "-" reads from
// opts.StdinReader (or iostreams.IO.In as the production default) for
// piped JSON payloads.
//
// The body is sent as a JSON document (the SDK marshals it as json.RawMessage),
// so a non-JSON / malformed payload is validated and rejected here as
// input.invalid_argument (exit 5). Without this, the failure surfaced from the
// SDK's marshal step as a confusing network.error (exit 7, "retryable") —
// looping an agent on a body that can never be sent.
func readInput(opts *Options) ([]byte, error) {
	var b []byte
	if opts.Input == "-" {
		r := opts.StdinReader
		if r == nil {
			r = iostreams.IO.In
		}
		var err error
		b, err = io.ReadAll(r)
		if err != nil {
			return nil, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read request body from stdin")
		}
	} else {
		var err error
		b, err = os.ReadFile(opts.Input)
		if err != nil {
			return nil, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read input file %s", opts.Input)
		}
	}
	if len(bytes.TrimSpace(b)) > 0 && !json.Valid(b) {
		return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"--input must be valid JSON (the request body is sent as a JSON document)")
	}
	return b, nil
}

// resolveBody returns the request body bytes from -d/--data (inline) or
// --input (file/stdin), or nil when neither is set. Inline --data is
// validated as JSON here; --input is validated inside readInput. The two
// flags are mutually exclusive — checked here (rather than cobra's
// MarkFlagsMutuallyExclusive, whose violation surfaces as an unclassified
// internal.error / exit 1) so the conflict reports input.invalid_argument.
func resolveBody(opts *Options) ([]byte, error) {
	set := 0
	if opts.Data != "" {
		set++
	}
	if opts.Input != "" {
		set++
	}
	if len(opts.Fields) > 0 {
		set++
	}
	if set > 1 {
		return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"-d/--data, --input, and -F/--field are mutually exclusive; supply the body one way")
	}
	if len(opts.Fields) > 0 {
		return buildFieldBody(opts.Fields)
	}
	if opts.Data != "" {
		if !json.Valid([]byte(opts.Data)) {
			return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				"-d/--data must be valid JSON (the request body is sent as a JSON document)")
		}
		return []byte(opts.Data), nil
	}
	if opts.Input != "" {
		return readInput(opts)
	}
	return nil, nil
}

// hasBody reports whether a request body was supplied via -d or --input.
func hasBody(opts *Options) bool {
	return opts.Data != "" || opts.Input != "" || len(opts.Fields) > 0
}

// inferFieldValue maps a -F value string to a typed JSON scalar: literal
// true/false/null and integer/float literals become typed JSON; everything
// else stays a string. A value that looks numeric but must stay a string
// (e.g. a zero-padded id "007") can't be forced here — use -d/--data or
// --input for full control.
func inferFieldValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}

// buildFieldBody assembles -F/--field key=value pairs into a JSON object with
// type inference. Flat keys only. Returns input.invalid_argument (exit 5) for
// a malformed pair or an unsupported @file value.
func buildFieldBody(fields []string) ([]byte, error) {
	obj := make(map[string]any, len(fields))
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				fmt.Sprintf("-F/--field must be key=value, got %q", f))
		}
		if strings.HasPrefix(v, "@") {
			return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				fmt.Sprintf("-F %q: @file values are not supported; read a file body with --input", f))
		}
		obj[k] = inferFieldValue(v)
	}
	return json.Marshal(obj)
}

// resolveMethod implements the auto-method behavior: explicit -X wins;
// otherwise body presence (-d/--data or --input) promotes GET → POST.
func resolveMethod(opts *Options) string {
	if opts.Method != "" {
		return strings.ToUpper(opts.Method)
	}
	if hasBody(opts) {
		return "POST"
	}
	return "GET"
}

// apiRetryArgv reconstructs a directly-executable `weknora api` argv (with -y)
// for the write-confirmation gate, preserving the method, path and body flags
// the caller passed so an agent can re-run the exact mutation after approval.
func apiRetryArgv(opts *Options, method, path string) []string {
	argv := []string{"weknora", "api", "-X", method, path}
	switch {
	case opts.Data != "":
		argv = append(argv, "-d", opts.Data)
	case opts.Input != "":
		argv = append(argv, "--input", opts.Input)
	default:
		for _, f := range opts.Fields {
			argv = append(argv, "-F", f)
		}
	}
	return append(argv, "-y")
}

// runAPI is the testable core: validate inputs, dispatch via Service.Raw,
// classify status, and emit either the raw body or a JSON object. The
// caller is responsible for resolving the method (defaults / auto-POST)
// and uppercasing it; runAPI guards against unsupported values like
// `-X PATCH-INVALID` reaching the wire.
//
// When paginate is true and method is GET, all offset-based pages are
// fetched and merged into a single {data, total} JSON response. For
// non-GET methods paginate is silently ignored (no offset semantic).
func runAPI(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, svc Service, method, path string, paginate bool) error {
	if paginate && method == http.MethodGet {
		return runAPIPaginated(ctx, opts, fopts, svc, path)
	}
	return runAPISingle(ctx, opts, fopts, svc, method, path)
}

// runAPISingle is the original single-call implementation of runAPI.
func runAPISingle(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, svc Service, method, path string) error {
	if method == "" {
		return cmdutil.NewFlagError(fmt.Errorf("--method cannot be empty"))
	}
	if !strings.HasPrefix(path, "/") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("path must start with /: %s", path))
	}

	// Resolve request body from -d/--data (inline) or --input (file / `-`).
	// Empty input is treated as no body rather than an empty JSON document
	// (which fails to marshal).
	contents, err := resolveBody(opts)
	if err != nil {
		return err
	}
	var body any
	if len(bytes.TrimSpace(contents)) > 0 {
		body = json.RawMessage(contents)
	}

	resp, err := svc.Raw(ctx, method, path, body)
	if err != nil {
		// Transport / DNS failure (Raw never returns a typed HTTP error of its
		// own; non-2xx responses still surface as resp != nil, err == nil).
		return cmdutil.WrapHTTP(err, "%s %s", method, path)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeNetworkError, err, "read response body")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := cmdutil.ClassifyHTTPStatus(resp.StatusCode)
		ce := cmdutil.NewError(code, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))))
		if v := resp.Header.Get("Retry-After"); v != "" {
			if s, perr := strconv.Atoi(v); perr == nil && s > 0 {
				ce = ce.WithRetryAfter(s)
			}
		}
		return ce
	}

	return emitRawBody(respBody, fopts)
}

// emitRawBody writes a server response body under the active format. JSON mode
// places the parsed body directly under envelope.data — no {status, headers,
// body} wrapper — so `--jq '.data...'` projects at the server's own shape
// (e.g. `.data.data[]` for a list endpoint) rather than a triple-nested
// `.data.body.data[]`; non-JSON decodes best-effort to a raw string. Text mode
// writes the body verbatim with a trailing newline. Shared by the single and
// paginated-fallback paths so both project identically.
func emitRawBody(body []byte, fopts *cmdutil.FormatOptions) error {
	out := iostreams.IO.Out
	if fopts.WantsJSON() {
		var bodyAny any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &bodyAny); err != nil {
				bodyAny = string(body)
			}
		}
		return fopts.Emit(out, bodyAny, nil)
	}
	if _, err := out.Write(body); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write response body")
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		_, _ = out.Write([]byte{'\n'})
	}
	return nil
}

// runAPIPaginated fetches all offset-based pages for a GET request and writes
// a single merged {data, total} JSON object to stdout. If the first page
// response does not carry pagination metadata (total + page_size), the raw
// response is passed through via passThroughFallback which respects the
// --format envelope contract (same shape as runAPISingle's fallback path).
func runAPIPaginated(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, svc Service, path string) error {
	if !strings.HasPrefix(path, "/") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("path must start with /: %s", path))
	}

	pageSize := extractPageSize(path)
	if pageSize == 0 {
		pageSize = 50
	}

	allData := []json.RawMessage{}
	var lastTotal int64
	page := 1

	for {
		curPath := setPageParam(path, page, pageSize)
		resp, err := svc.Raw(ctx, http.MethodGet, curPath, nil)
		if err != nil {
			return cmdutil.WrapHTTP(err, "GET %s", curPath)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			code := cmdutil.ClassifyHTTPStatus(resp.StatusCode)
			return cmdutil.NewError(code, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		}

		var pageResp struct {
			Data     []json.RawMessage `json:"data"`
			Total    int64             `json:"total"`
			Page     int               `json:"page"`
			PageSize int               `json:"page_size"`
		}
		if err := json.Unmarshal(body, &pageResp); err != nil {
			// Non-JSON response on first page — fall back through the envelope.
			if page == 1 {
				return emitRawBody(body, fopts)
			}
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				fmt.Sprintf("--paginate: page %d response not in expected shape: %v", page, err))
		}

		// Heuristic: if the first page lacks pagination metadata, treat the
		// response as non-paginated and fall back through the envelope.
		if page == 1 && pageResp.Total == 0 && pageResp.PageSize == 0 {
			return emitRawBody(body, fopts)
		}

		allData = append(allData, pageResp.Data...)
		lastTotal = pageResp.Total

		// Termination: accumulated count (not page*pageSize) handles server-capped page sizes.
		if int64(len(allData)) >= pageResp.Total || len(pageResp.Data) == 0 {
			break
		}
		page++
	}

	merged := map[string]any{
		"data":  allData,
		"total": lastTotal,
	}
	return fopts.Emit(iostreams.IO.Out, merged, nil)
}

// extractPageSize parses the page_size query parameter from path, returning 0
// if absent or unparseable.
func extractPageSize(path string) int {
	u, err := url.Parse(path)
	if err != nil {
		return 0
	}
	if v := u.Query().Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// setPageParam rewrites the page and page_size query parameters in path,
// preserving all other query parameters.
func setPageParam(path string, page, pageSize int) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	u.RawQuery = q.Encode()
	return u.String()
}

// compile-time check: the production SDK client implements Service.
var _ Service = (*sdk.Client)(nil)
