package auth

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// authTokenFields lists fields available for `auth token --json=` projection.
// Single-resource envelope shape (data is the token result, not data.items).
var authTokenFields = []string{"token", "mode", "context"}

type tokenResult struct {
	Token   string `json:"token"`
	Mode    string `json:"mode"` // ModeBearer (JWT) or ModeAPIKey
	Context string `json:"context"`
}

// NewCmdToken builds `weknora auth token`. Prints the active context's
// credential to stdout for use in shell pipelines, e.g.
//
//	WEKNORA_TOKEN=$(weknora auth token)
//	curl -H "Authorization: Bearer $WEKNORA_TOKEN" ...     # JWT mode
//	curl -H "X-API-Key: $WEKNORA_TOKEN" ...                # api-key mode
//
// The user is responsible for constructing the appropriate header —
// `auth list` shows which mode each context uses.
//
// Default output: raw token on stdout, no trailing newline (clean $(...)).
// `--json[=fields]` wraps in envelope {token, mode, context}.
func NewCmdToken(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the active context's credential to stdout",
		Long: `Print the active context's credential to stdout, with no trailing
newline, suitable for shell command substitution.

The credential is the long-lived API key (mode: api-key) or the access JWT
(mode: bearer), depending on how the context was created. Run ` + "`weknora auth list`" + `
to see which mode each context uses, and construct the matching HTTP header:

  Authorization: Bearer <token>    # bearer mode
  X-API-Key: <token>               # api-key mode

` + "`--context <name>`" + ` (global flag) selects a non-active context to read from.`,
		Example: `  WEKNORA_TOKEN=$(weknora auth token)
  weknora auth token --context staging
  weknora auth token --json`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			return runToken(f, jopts)
		},
	}
	cmdutil.AddJSONFlags(cmd, authTokenFields)
	aiclient.SetAgentHelp(cmd, "Prints the active context's credential to stdout for scripting. Default: raw secret, no trailing newline. With --json: envelope {token, mode, context}. Errors: auth.unauthenticated when no active context or no stored credential (run `auth login`); local.keychain_denied when the keyring rejects the read.")
	return cmd
}

func runToken(f *cmdutil.Factory, jopts *cmdutil.JSONOptions) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	ctxName := cfg.CurrentContext
	if f.ContextOverride != "" {
		ctxName = f.ContextOverride
	}
	if ctxName == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			"no current context configured")
	}
	ctx, ok := cfg.Contexts[ctxName]
	if !ok {
		return cmdutil.NewError(cmdutil.CodeLocalContextNotFound,
			fmt.Sprintf("context %q not found", ctxName))
	}

	store, err := f.Secrets()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "init secrets store")
	}

	// Resolve the stored credential. Prefer bearer (JWT) when both refs
	// are present — JWT is the more capable mode and what `buildClient`
	// uses for the Authorization header (see factory.go buildClient).
	var token, mode string
	switch {
	case ctx.TokenRef != "":
		v, ferr := cmdutil.LoadSecret(store, ctxName, "access")
		if ferr != nil {
			return ferr
		}
		token, mode = v, ModeBearer
	case ctx.APIKeyRef != "":
		v, ferr := cmdutil.LoadSecret(store, ctxName, "api_key")
		if ferr != nil {
			return ferr
		}
		token, mode = v, ModeAPIKey
	default:
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			fmt.Sprintf("context %q has no stored credential; run `weknora auth login`", ctxName))
	}

	if token == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			fmt.Sprintf("context %q credential is empty in keyring; run `weknora auth login`", ctxName))
	}

	if jopts.Enabled() {
		return format.WriteEnvelopeFiltered(iostreams.IO.Out,
			format.Success(tokenResult{Token: token, Mode: mode, Context: ctxName},
				&format.Meta{Context: ctxName}),
			jopts.Fields, jopts.JQ)
	}

	// No trailing newline — clean $(weknora auth token) substitution.
	fmt.Fprint(iostreams.IO.Out, token)
	// Defensive hint to stderr when stdout is an interactive terminal:
	// the user likely didn't mean to display the secret on screen.
	// stderr-only so scripts (always non-TTY) are unaffected. Mode-specific
	// because api-key tokens are long-lived and rotation is the only
	// recourse on leak — bearer tokens self-expire via refresh.
	if iostreams.IO.IsStdoutTTY() {
		fmt.Fprintln(iostreams.IO.Err)
		fmt.Fprintln(iostreams.IO.Err, "hint: pipe to $(weknora auth token) to capture; this terminal scrollback now contains the secret")
		if mode == ModeAPIKey {
			fmt.Fprintln(iostreams.IO.Err, "note: api-key credentials are long-lived — rotate via your auth provider if exposed (no `auth refresh` path)")
		}
	}
	return nil
}
