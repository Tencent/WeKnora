// Package api implements the `weknora api` raw HTTP passthrough command.
//
// Shape: one positional (path) + `-X/--method` flag, default GET (auto-
// promoted to POST when a body is supplied via --data or --input). The two
// body-source flags are mutually exclusive. Default raw response body to
// stdout; --format json emits a {status, headers, body} object. Reuses sdk.Client.Raw which already
// applies tenant + auth headers.
package api

import (
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
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// apiFields is intentionally a marker - api wraps arbitrary HTTP responses
// whose schema the CLI doesn't know, so field hints are meaningless here.
// The marker shows up in --help so users can tell.
var apiFields = []string{"<response-shape-varies>"}

type Options struct {
	Method      string
	Data        string
	Input       string // --input: file path, "-" for stdin
	Yes         bool
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
		Long: `Send an HTTP request through the SDK and print the response.

The default method is GET; passing --data / --input auto-promotes it to
POST. Use -X/--method to override (DELETE / PUT / PATCH / HEAD).

Auth, tenant, and request-id headers are applied automatically from the
active context. The response body is written to stdout by default; use
--format json to emit a {status, headers, body} JSON object.

Examples:
  weknora api /api/v1/knowledge-bases                              # GET
  weknora api /api/v1/knowledge-bases --data '{"name":"foo"}'      # POST (auto)
  weknora api /api/v1/knowledge-bases/kb_xxx -X DELETE`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			method := resolveMethod(opts)
			// Escape-hatch DELETE through `weknora api` is just as destructive
			// as `weknora kb delete` - exit-10 protocol must apply (cli/README.md).
			if method == http.MethodDelete {
				if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(), "endpoint", args[0]); err != nil {
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
	cmd.Flags().StringVarP(&opts.Method, "method", "X", "", "HTTP method (default: GET, or POST when a body is supplied)")
	cmd.Flags().StringVarP(&opts.Data, "data", "d", "", "Request body as raw string (e.g. JSON)")
	cmd.Flags().StringVar(&opts.Input, "input", "", "Read request body from file (use `-` for stdin)")
	cmd.Flags().Bool("paginate", false, "Follow offset-based pagination (?page=N&page_size=M), merging all pages into a single {data, total} JSON response.")
	cmdutil.AddFormatFlag(cmd, apiFields...)
	cmd.MarkFlagsMutuallyExclusive("data", "input")
	return cmd
}

// readInput reads opts.Input and returns its contents. "-" reads from
// opts.StdinReader (or iostreams.IO.In as the production default) for
// piped JSON payloads.
func readInput(opts *Options) ([]byte, error) {
	if opts.Input == "-" {
		r := opts.StdinReader
		if r == nil {
			r = iostreams.IO.In
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read request body from stdin")
		}
		return b, nil
	}
	b, err := os.ReadFile(opts.Input)
	if err != nil {
		return nil, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read input file %s", opts.Input)
	}
	return b, nil
}

// resolveMethod implements the auto-method behavior: explicit -X wins;
// otherwise body presence promotes GET → POST.
func resolveMethod(opts *Options) string {
	if opts.Method != "" {
		return strings.ToUpper(opts.Method)
	}
	if opts.Data != "" || opts.Input != "" {
		return "POST"
	}
	return "GET"
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
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("unsupported method: %s", method))
	}
	if !strings.HasPrefix(path, "/") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("path must start with /: %s", path))
	}

	// Resolve request body. --data and --input are mutually exclusive at
	// the cobra layer; the second branch is reachable only when --data is
	// empty.
	var body any
	if opts.Data != "" {
		body = json.RawMessage(opts.Data)
	} else if opts.Input != "" {
		contents, err := readInput(opts)
		if err != nil {
			return err
		}
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
		return cmdutil.NewError(code, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))))
	}

	out := iostreams.IO.Out
	if fopts.WantsJSON() {
		// Best-effort decode: if response body is valid JSON, surface the
		// parsed structure under .body so JSON consumers can drill
		// in; otherwise fall back to the raw string.
		var bodyAny any
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, &bodyAny); err != nil {
				bodyAny = string(respBody)
			}
		}
		hdrs := make(map[string]string, len(resp.Header))
		for k, v := range resp.Header {
			if len(v) > 0 {
				hdrs[k] = v[0]
			}
		}
		// --jq runs over the full {status, headers, body} object. Per-field
		// projection isn't meaningful here since the response schema is opaque
		// to the CLI.
		return format.WriteJSONFiltered(out, map[string]any{
			"status":  resp.StatusCode,
			"headers": hdrs,
			"body":    bodyAny,
		}, nil, fopts.JQ)
	}

	if _, err := out.Write(respBody); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write response body")
	}
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' {
		_, _ = out.Write([]byte{'\n'})
	}
	return nil
}

// runAPIPaginated fetches all offset-based pages for a GET request and writes
// a single merged {data, total} JSON object to stdout. If the first page
// response does not carry pagination metadata (total + page_size), the raw
// response is passed through unchanged (single-call fallback).
func runAPIPaginated(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, svc Service, path string) error {
	if !strings.HasPrefix(path, "/") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("path must start with /: %s", path))
	}

	pageSize := extractPageSize(path)
	if pageSize == 0 {
		pageSize = 50
	}

	var allData []json.RawMessage
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
			// Non-JSON response on first page — pass through verbatim.
			if page == 1 {
				return passThroughRaw(body)
			}
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				fmt.Sprintf("--paginate: page %d response not in expected shape: %v", page, err))
		}

		// Heuristic: if the first page lacks pagination metadata, treat the
		// response as non-paginated and pass through verbatim.
		if page == 1 && pageResp.Total == 0 && pageResp.PageSize == 0 {
			return passThroughRaw(body)
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
	return fopts.Emit(iostreams.IO.Out, merged)
}

// passThroughRaw writes body verbatim to stdout (appending a newline if
// absent), mirroring the single-call passthrough path.
func passThroughRaw(body []byte) error {
	out := iostreams.IO.Out
	if _, err := out.Write(body); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write response body")
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		_, _ = out.Write([]byte{'\n'})
	}
	return nil
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
