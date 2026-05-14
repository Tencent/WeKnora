// Package api implements the `weknora api` raw HTTP passthrough command.
//
// Shape: one positional (path) + `-X/--method` flag, default GET (auto-
// promoted to POST when a body is supplied via --data or --input). The two
// body-source flags are mutually exclusive. Default raw response body to
// stdout; --json emits a {status, headers, body} object. Reuses sdk.Client.Raw which already
// applies tenant + auth headers.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// apiFields is intentionally a marker — api wraps arbitrary HTTP responses
// whose schema the CLI doesn't know, so the `--json=id,name` field-filter
// is a no-op here. The marker shows up in --help so users can tell.
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
--json to emit a {status, headers, body} JSON object.

Examples:
  weknora api /api/v1/knowledge-bases                              # GET
  weknora api /api/v1/knowledge-bases --data '{"name":"foo"}'      # POST (auto)
  weknora api /api/v1/knowledge-bases/kb_xxx -X DELETE`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			method := resolveMethod(opts)
			// Escape-hatch DELETE through `weknora api` is just as destructive
			// as `weknora kb delete` — exit-10 protocol must apply (AGENTS.md).
			if method == http.MethodDelete {
				if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, jopts.Enabled(), "endpoint", args[0]); err != nil {
					return err
				}
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runAPI(c.Context(), opts, jopts, cli, method, args[0])
		},
	}
	cmd.Flags().StringVarP(&opts.Method, "method", "X", "", "HTTP method (default: GET, or POST when a body is supplied)")
	cmd.Flags().StringVarP(&opts.Data, "data", "d", "", "Request body as raw string (e.g. JSON)")
	cmd.Flags().StringVar(&opts.Input, "input", "", "Read request body from file (use `-` for stdin)")
	cmdutil.AddJSONFlags(cmd, apiFields)
	cmd.MarkFlagsMutuallyExclusive("data", "input")
	aiclient.SetAgentHelp(cmd, "Raw HTTP passthrough. Auth / tenant / request-id headers injected from the active context. --json wraps as {status, headers, body}; --json field filtering is ignored here (response shape unknown to CLI) — use --jq for reshape. DELETE triggers exit-10 confirm. Non-2xx maps to typed errors via ClassifyHTTPStatus (4xx → input.invalid_argument / auth.* / resource.not_found; 5xx → server.error). --data and --input are mutually exclusive.")
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
func runAPI(ctx context.Context, opts *Options, jopts *cmdutil.JSONOptions, svc Service, method, path string) error {
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
	if jopts.Enabled() {
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
		// --json field-filter is ignored (response shape unknown to the
		// CLI); --jq runs over the full {status, headers, body} object.
		return format.WriteJSONFiltered(out, map[string]any{
			"status":  resp.StatusCode,
			"headers": hdrs,
			"body":    bodyAny,
		}, nil, jopts.JQ)
	}

	if _, err := out.Write(respBody); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write response body")
	}
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' {
		_, _ = out.Write([]byte{'\n'})
	}
	return nil
}

// compile-time check: the production SDK client implements Service.
var _ Service = (*sdk.Client)(nil)
