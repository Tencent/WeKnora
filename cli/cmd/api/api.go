// Package api implements the `weknora api` raw HTTP passthrough command.
//
// Mirrors `gh api` ergonomics: 2 positionals (method, path), --data /
// --data-file mutually exclusive body source, default raw response body to
// stdout, --json wraps in CLI envelope. Reuses sdk.Client.Raw which already
// applies tenant + auth headers; v0.2 does not support --header (SDK Raw
// signature lacks header param) — that's planned for v0.3.
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

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// Options captures `weknora api` flag state.
type Options struct {
	Data     string
	DataFile string
	JSONOut  bool
	DryRun   bool
	Yes      bool
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
		Use:   "api <method> <path>",
		Short: "Make a raw API request to the WeKnora server",
		Long: `Send an HTTP request through the SDK and print the response.

Auth, tenant, and request-id headers are applied automatically from the active
context. The response body is written to stdout by default; use --json to wrap
it in the CLI envelope (status / headers / body).

Examples:
  weknora api GET /api/v1/knowledge-bases
  weknora api POST /api/v1/knowledge-bases --data '{"name":"foo"}'
  weknora api DELETE /api/v1/knowledge-bases/kb_xxx`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			opts.DryRun = cmdutil.IsDryRun(c)
			opts.Yes, _ = c.Flags().GetBool("yes")
			method := strings.ToUpper(args[0])
			// Escape-hatch DELETE through `weknora api` is just as destructive
			// as `weknora kb delete` — exit-10 protocol must apply (AGENTS.md).
			// Dry-run is read-only preview, so it skips confirmation.
			if !opts.DryRun && method == "DELETE" {
				if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, opts.JSONOut, "endpoint", args[1]); err != nil {
					return err
				}
			}
			if opts.DryRun {
				return runAPI(c.Context(), opts, nil, args[0], args[1])
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runAPI(c.Context(), opts, cli, args[0], args[1])
		},
	}
	cmd.Flags().StringVarP(&opts.Data, "data", "d", "", "Request body as raw string (e.g. JSON)")
	cmd.Flags().StringVar(&opts.DataFile, "data-file", "", "Read request body from file")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Wrap response in JSON envelope (status/headers/body)")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	agent.SetAgentHelp(cmd, "Raw HTTP passthrough to the WeKnora server. Use when no typed command exists for the endpoint. Headers (auth / tenant / request-id) are injected from the active context.")
	return cmd
}

// runAPI is the testable core: validate inputs, dispatch via Service.Raw,
// classify status, and emit either the raw body or a JSON envelope.
func runAPI(ctx context.Context, opts *Options, svc Service, methodArg, path string) error {
	method := strings.ToUpper(methodArg)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("unsupported method: %s", methodArg))
	}
	if !strings.HasPrefix(path, "/") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("path must start with /: %s", path))
	}

	// Resolve request body. --data and --data-file are mutually exclusive at
	// the cobra layer; the second branch is reachable only when --data is
	// empty.
	var body any
	if opts.Data != "" {
		body = json.RawMessage(opts.Data)
	} else if opts.DataFile != "" {
		contents, err := os.ReadFile(opts.DataFile)
		if err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read data file %s", opts.DataFile)
		}
		body = json.RawMessage(contents)
	}

	// --dry-run only meaningful for write methods; GET/HEAD have no side
	// effect to preview, so we proceed normally even with --dry-run.
	if opts.DryRun && method != "GET" && method != "HEAD" {
		level := format.RiskWrite
		if method == "DELETE" {
			level = format.RiskHighRiskWrite
		}
		preview := map[string]any{"method": method, "path": path}
		if body != nil {
			preview["body"] = body
		}
		return cmdutil.EmitDryRun(opts.JSONOut, preview, nil,
			&format.Risk{Level: level, Action: fmt.Sprintf("%s %s", method, path)})
	}

	resp, err := svc.Raw(ctx, method, path, body)
	if err != nil {
		// Transport / DNS failure (Raw never returns a typed HTTP error of its
		// own; non-2xx responses still surface as resp != nil, err == nil).
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "%s %s", method, path)
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
	if opts.JSONOut {
		// Best-effort decode: if response body is valid JSON, surface the
		// parsed structure under .data.body so envelope consumers can drill
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
		env := format.Success(map[string]any{
			"status":  resp.StatusCode,
			"headers": hdrs,
			"body":    bodyAny,
		}, nil)
		return format.WriteEnvelope(out, env)
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
