package auth

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// authLoginFields enumerates the fields surfaced for `--format json` discovery on
// `auth login`. The post-login summary has no token values - they stay in the
// keyring; agents who need to verify the credential should re-run
// `auth status`.
var authLoginFields = []string{
	"profile", "host", "mode", "user", "tenant_id",
}

// LoginOptions is the configuration captured from flags + prompts.
type LoginOptions struct {
	Host        string // --host
	Profile     string // --name: profile name to write into config.yaml
	WithToken   bool   // --with-token: read api key from stdin instead of prompting password
	APIKey      string // populated by --with-token from stdin
	Email       string
	Password    string
	StdinReader io.Reader // override for tests
}

// LoginService is the narrow SDK surface auth login depends on.
// *sdk.Client satisfies it implicitly via the new Login(ctx, LoginRequest)
// signature added in client/auth.go.
type LoginService interface {
	Login(ctx context.Context, req sdk.LoginRequest) (*sdk.LoginResponse, error)
}

// apiKeyValidator probes /auth/me with the supplied API key so a bad key
// fails fast at `auth login --with-token` time rather than on the next
// authenticated call.
//
// Returns the resolved user (used to populate Profile.User / TenantID at
// rest, so later `auth list` reflects who owns the key).
type apiKeyValidator func(ctx context.Context, host, apiKey string) (*sdk.AuthUser, error)

// defaultAPIKeyValidator builds a one-shot SDK client with the supplied key
// and calls /auth/me. Side-effect-free; no persistence.
var defaultAPIKeyValidator apiKeyValidator = func(ctx context.Context, host, apiKey string) (*sdk.AuthUser, error) {
	resp, err := sdk.NewClient(host, sdk.WithAPIKey(apiKey)).GetCurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	if !resp.Success || resp.Data.User == nil {
		return nil, fmt.Errorf("server rejected the API key (no user returned)")
	}
	return resp.Data.User, nil
}

// NewCmdLogin builds the `weknora auth login` command. runF is the testable
// entrypoint (left nil for production; see cli/cmd/auth/login_test.go).
func NewCmdLogin(f *cmdutil.Factory, runF func(context.Context, *LoginOptions, *cmdutil.FormatOptions, *cmdutil.Factory, LoginService) error) *cobra.Command {
	opts := &LoginOptions{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a WeKnora server and persist credentials",
		Long: `Log in by email + password (interactive prompt) or pipe an API key with --with-token.

Credentials are persisted to the OS keyring when available; otherwise to a
0600 file under $XDG_CONFIG_HOME/weknora/secrets. The named profile becomes
the current_profile in ~/.config/weknora/config.yaml.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			run := runF
			if run == nil {
				run = runLogin
			}
			svc := loginServiceFor(opts.Host)
			if opts.StdinReader == nil {
				opts.StdinReader = iostreams.IO.In
			}
			return run(c.Context(), opts, fopts, f, svc)
		},
	}
	cmd.Flags().StringVar(&opts.Host, "host", "", "WeKnora server URL, e.g. https://kb.example.com")
	cmd.Flags().StringVar(&opts.Profile, "name", "default", "Profile name to register in config.yaml")
	cmd.Flags().BoolVar(&opts.WithToken, "with-token", false, "Read an API key from stdin instead of prompting for password")
	cmdutil.AddFormatFlag(cmd, authLoginFields...)
	_ = cmd.MarkFlagRequired("host")
	return cmd
}

// loginServiceFor returns a fresh SDK client targeting host. login.go cannot
// reuse Factory.Client because that closure requires an existing profile.
func loginServiceFor(host string) LoginService {
	if host == "" {
		return nil
	}
	return sdk.NewClient(host)
}

func runLogin(ctx context.Context, opts *LoginOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, svc LoginService) error {
	if err := validateHost(opts.Host); err != nil {
		return err
	}
	// Reject shell-metacharacter / path-like names up-front so opts.Profile
	// stays safe to interpolate into the keyring namespace, config.yaml
	// keys, and (later) envelope.error.retry_command. Matches `profile add`.
	if err := cmdutil.ValidateProfileName(opts.Profile); err != nil {
		return err
	}

	if opts.WithToken {
		key, err := readStdinTrimmed(opts.StdinReader)
		if err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read stdin")
		}
		if key == "" {
			return cmdutil.NewError(cmdutil.CodeInputMissingFlag, "--with-token requires an API key piped to stdin")
		}
		opts.APIKey = key
		// Validate against the server before persisting so a typo'd /
		// expired / wrong-host key fails fast at login time. The probe
		// is /auth/me - read-only, side-effect-free.
		user, err := defaultAPIKeyValidator(ctx, opts.Host, key)
		if err != nil {
			// Transport errors (connection refused, DNS failure) must not be
			// surfaced as auth.bad_credential — the key may be fine but the
			// host is unreachable. Classify via WrapHTTP so network errors
			// get CodeNetworkError and the hint points at `weknora doctor`.
			if cmdutil.ClassifyHTTPError(err) == cmdutil.CodeNetworkError {
				return cmdutil.Wrapf(cmdutil.CodeNetworkError, err, "validate API key: check host reachability")
			}
			return cmdutil.Wrapf(cmdutil.CodeAuthBadCredential, err, "validate API key")
		}
		return persistAPIKey(opts, fopts, f, user)
	}

	// Interactive: prompt for email + password.
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "login: no SDK client (host missing?)")
	}
	if opts.Email == "" || opts.Password == "" {
		p := f.Prompter()
		if opts.Email == "" {
			email, err := p.Input("Email", "")
			if err != nil {
				return cmdutil.Wrapf(cmdutil.CodeInputMissingFlag, err, "email prompt")
			}
			opts.Email = email
		}
		if opts.Password == "" {
			pw, err := p.Password("Password")
			if err != nil {
				return cmdutil.Wrapf(cmdutil.CodeInputMissingFlag, err, "password prompt")
			}
			opts.Password = pw
		}
	}

	resp, err := svc.Login(ctx, sdk.LoginRequest{Email: opts.Email, Password: opts.Password})
	if err != nil {
		// Transport errors (connection refused, DNS failure) must not be
		// surfaced as auth.bad_credential — credentials may be fine but the
		// host is unreachable. Classify so network errors get CodeNetworkError.
		if cmdutil.ClassifyHTTPError(err) == cmdutil.CodeNetworkError {
			return cmdutil.Wrapf(cmdutil.CodeNetworkError, err, "login: check host reachability")
		}
		return cmdutil.Wrapf(cmdutil.CodeAuthBadCredential, err, "login")
	}
	if !resp.Success || resp.Token == "" {
		return cmdutil.NewError(cmdutil.CodeAuthBadCredential, fmt.Sprintf("login refused: %s", resp.Message))
	}

	return persistJWT(opts, fopts, f, resp)
}

// persistAPIKey saves the --with-token API key and writes the profile.
// user is the principal returned by /auth/me during pre-persist validation,
// used to populate Profile.User / TenantID so `auth list` reflects who
// owns the key.
func persistAPIKey(opts *LoginOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, user *sdk.AuthUser) error {
	store, err := f.Secrets()
	if err != nil {
		return err
	}
	warnOnFileFallback(store)
	if err := store.Set(opts.Profile, "api_key", opts.APIKey); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save api key")
	}
	prof := &config.Profile{
		Host:      opts.Host,
		APIKeyRef: store.Ref(opts.Profile, "api_key"),
	}
	if user != nil {
		prof.User = user.Email
		prof.TenantID = user.TenantID
	}
	return saveProfileRef(opts, fopts, f, prof, user)
}

// persistJWT saves access + refresh tokens and writes the profile.
func persistJWT(opts *LoginOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, resp *sdk.LoginResponse) error {
	store, err := f.Secrets()
	if err != nil {
		return err
	}
	warnOnFileFallback(store)
	if err := store.Set(opts.Profile, "access", resp.Token); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save access token")
	}
	if resp.RefreshToken != "" {
		if err := store.Set(opts.Profile, "refresh", resp.RefreshToken); err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save refresh token")
		}
	}
	prof := &config.Profile{
		Host:       opts.Host,
		TokenRef:   store.Ref(opts.Profile, "access"),
		RefreshRef: store.Ref(opts.Profile, "refresh"),
	}
	if resp.User != nil {
		prof.User = resp.User.Email
		prof.TenantID = resp.User.TenantID
	}
	return saveProfileRef(opts, fopts, f, prof, resp.User)
}

// loginResult is the typed payload emitted by `--format json`. mode is derived from
// whether the server returned a user (password flow) vs API-key flow.
type loginResult struct {
	Profile  string `json:"profile"`
	Host     string `json:"host"`
	Mode     string `json:"mode"` // ModeBearer or ModeAPIKey
	User     string `json:"user,omitempty"`
	TenantID uint64 `json:"tenant_id,omitempty"`
}

// saveProfileRef writes the profile to config.yaml and prints success.
func saveProfileRef(opts *LoginOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, prof *config.Profile, user *sdk.AuthUser) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.Profile{}
	}
	cfg.Profiles[opts.Profile] = *prof
	cfg.CurrentProfile = opts.Profile
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}
	if fopts.WantsJSON() {
		result := loginResult{Profile: opts.Profile, Host: opts.Host, Mode: ModeAPIKey}
		if user != nil {
			result.Mode = ModeBearer
			result.User = user.Email
			result.TenantID = user.TenantID
		}
		return fopts.Emit(iostreams.IO.Out, result, nil)
	}
	who := opts.Profile
	if user != nil {
		who = user.Email
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Logged in to %s as %s (profile=%s)\n", opts.Host, who, opts.Profile)
	return nil
}

// validateHost rejects empty / non-http URLs early so we surface a clean
// flag error instead of failing inside the SDK transport.
func validateHost(host string) error {
	_, err := cmdutil.NormalizeHost(host)
	return err
}

// warnOnFileFallback prints a one-shot stderr advisory when the secrets
// store fell back to the plaintext 0600 file backend (keychain unavailable
// - typical on headless CI, WSL without DBus, agent containers). Helps
// users notice that credentials are NOT in the OS keychain before they're
// surprised by it later. doctor's credential_storage check carries the
// same info but agents that bypass doctor would otherwise miss it.
func warnOnFileFallback(store secrets.Store) {
	if _, isFile := store.(*secrets.FileStore); !isFile {
		return
	}
	fmt.Fprintln(iostreams.IO.Err, "warning: OS keychain unavailable - credentials will be saved to a 0600 file under $XDG_CONFIG_HOME/weknora/secrets/.")
	fmt.Fprintln(iostreams.IO.Err, "         install / unlock the keyring (or use `weknora doctor` to inspect) for OS-backed storage.")
}

// readStdinTrimmed reads all of r and returns the result with surrounding
// whitespace stripped. Empty result is returned as-is for the caller to
// validate.
func readStdinTrimmed(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
