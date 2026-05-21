package auth

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// authTokenFields lists fields surfaced in `--help` as a hint for `--jq`
// projection. Single-resource shape: emits the bare token object directly.
var authTokenFields = []string{"token", "mode", "profile"}

type tokenResult struct {
	Token   string `json:"token"`
	Mode    string `json:"mode"` // ModeBearer (JWT) or ModeAPIKey
	Profile string `json:"profile"`
}

// NewCmdToken builds `weknora auth token`. Prints the active profile's
// credential to stdout for use in shell pipelines, e.g.
//
//	WEKNORA_TOKEN=$(weknora auth token)
//	curl -H "Authorization: Bearer $WEKNORA_TOKEN" ...     # JWT mode
//	curl -H "X-API-Key: $WEKNORA_TOKEN" ...                # api-key mode
//
// The user is responsible for constructing the appropriate header -
// `auth list` shows which mode each profile uses.
//
// Default output: raw token on stdout, no trailing newline (clean $(...)).
// `--format json[=fields]` emits a bare {token, mode, profile} object.
func NewCmdToken(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the active profile's credential to stdout",
		Long: `Print the active profile's credential to stdout, with no trailing
newline, suitable for shell command substitution.

The credential is the long-lived API key (mode: api-key) or the access JWT
(mode: bearer), depending on how the profile was created. Run ` + "`weknora auth list`" + `
to see which mode each profile uses, and construct the matching HTTP header:

  Authorization: Bearer <token>    # bearer mode
  X-API-Key: <token>               # api-key mode

` + "`--profile <name>`" + ` (global flag) selects a non-active profile to read from.`,
		Example: `  WEKNORA_TOKEN=$(weknora auth token)
  weknora auth token --profile staging
  weknora auth token --format json`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runToken(f, fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, authTokenFields...)
	return cmd
}

func runToken(f *cmdutil.Factory, fopts *cmdutil.FormatOptions) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	profileName := cfg.CurrentProfile
	if f.ProfileOverride != "" {
		profileName = f.ProfileOverride
	}
	if profileName == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			"no current profile configured")
	}
	ctx, ok := cfg.Profiles[profileName]
	if !ok {
		return cmdutil.NewError(cmdutil.CodeLocalProfileNotFound,
			fmt.Sprintf("profile %q not found", profileName))
	}

	store, err := f.Secrets()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "init secrets store")
	}

	// Resolve the stored credential. Prefer bearer (JWT) when both refs
	// are present - JWT is the more capable mode and what `buildClient`
	// uses for the Authorization header (see factory.go buildClient).
	var token, mode string
	switch {
	case ctx.TokenRef != "":
		v, ferr := cmdutil.LoadSecret(store, profileName, "access")
		if ferr != nil {
			return ferr
		}
		token, mode = v, ModeBearer
	case ctx.APIKeyRef != "":
		v, ferr := cmdutil.LoadSecret(store, profileName, "api_key")
		if ferr != nil {
			return ferr
		}
		token, mode = v, ModeAPIKey
	default:
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			fmt.Sprintf("profile %q has no stored credential; run `weknora auth login`", profileName))
	}

	if token == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			fmt.Sprintf("profile %q credential is empty in keyring; run `weknora auth login`", profileName))
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, tokenResult{Token: token, Mode: mode, Profile: profileName}, nil)
	}

	// No trailing newline - clean $(weknora auth token) substitution.
	fmt.Fprint(iostreams.IO.Out, token)
	// Defensive hint to stderr when stdout is an interactive terminal:
	// the user likely didn't mean to display the secret on screen.
	// stderr-only so scripts (always non-TTY) are unaffected. Mode-specific
	// because api-key tokens are long-lived and rotation is the only
	// recourse on leak - bearer tokens self-expire via refresh.
	if iostreams.IO.IsStdoutTTY() {
		fmt.Fprintln(iostreams.IO.Err)
		fmt.Fprintln(iostreams.IO.Err, "hint: pipe to $(weknora auth token) to capture; this terminal scrollback now contains the secret")
		if mode == ModeAPIKey {
			fmt.Fprintln(iostreams.IO.Err, "note: api-key credentials are long-lived - rotate via your auth provider if exposed (no `auth refresh` path)")
		}
	}
	return nil
}
