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

// LoginOptions is the configuration captured from flags + prompts. Host and
// Profile are resolved from the active profile in config (not from flags) -
// `auth login` authenticates the already-existing active profile, created
// beforehand with `weknora profile add <name> --host <h> --use`.
type LoginOptions struct {
	Host        string // resolved from the active profile's Host in config
	Profile     string // resolved active profile name (honors global --profile)
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
		Short: "Authenticate the active profile against its WeKnora server",
		Long: `Authenticate the active profile by email + password (interactive prompt) or
pipe an API key with --with-token.

` + "`auth login`" + ` operates on the active profile (override with the global
--profile flag). Create the profile first with
` + "`weknora profile add <name> --host <h> --use`" + `, then run ` + "`weknora auth login`" + `.

Credentials are persisted to the OS keyring when available; otherwise to a
0600 file under $XDG_CONFIG_HOME/weknora/secrets.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			if opts.StdinReader == nil {
				opts.StdinReader = iostreams.IO.In
			}
			if runF != nil {
				// Test seam: the injected runF supplies its own service and
				// (where needed) seeds opts.Host / opts.Profile itself.
				return runF(c.Context(), opts, fopts, f, nil)
			}
			return runLogin(c.Context(), opts, fopts, f, nil)
		},
	}
	cmd.Flags().BoolVar(&opts.WithToken, "with-token", false, "Read an API key from stdin instead of prompting for password")
	cmdutil.AddFormatFlag(cmd, authLoginFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "authenticate the active profile; --with-token reads an API key from stdin (non-interactive)",
		Examples: []string{`echo "$WEKNORA_API_KEY" | weknora auth login --with-token`},
		Output:   "envelope.data is {profile, host, mode, user, tenant_id} on success",
		Warnings: []string{
			"a profile must exist and be active first (`weknora profile add <n> --host <url> --use`)",
			"password login is interactive-only (no flags) — agents must use --with-token with the key piped to stdin",
		},
	})
	return cmd
}

// resolveActiveProfile loads config and resolves the active profile (honoring
// the global --profile override) plus its persisted Host. `auth login`
// authenticates an already-existing profile; profile creation is `profile
// add`'s job. Returns typed errors when no active profile is configured or
// the profile lacks a host.
func resolveActiveProfile(f *cmdutil.Factory) (name, host string, err error) {
	// f.Config() already folds the global --profile / WEKNORA_PROFILE override
	// into cfg.CurrentProfile (same source f.ActiveProfile reads), so one load
	// resolves the active profile — matches logout/refresh.
	cfg, err := f.Config()
	if err != nil {
		return "", "", err
	}
	active := cfg.CurrentProfile
	if active == "" {
		return "", "", cmdutil.NewError(cmdutil.CodeAuthUnauthenticated,
			"no active profile; run `weknora profile add <name> --host <h> --use` first")
	}
	prof, ok := cfg.Profiles[active]
	if !ok {
		return "", "", cmdutil.NewError(cmdutil.CodeLocalProfileNotFound,
			fmt.Sprintf("active profile %q not found in config", active))
	}
	if prof.Host == "" {
		return "", "", cmdutil.NewError(cmdutil.CodeLocalConfigCorrupt,
			fmt.Sprintf("profile %q has no host", active))
	}
	return active, prof.Host, nil
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
	// Resolve the target profile + host from the active profile in config.
	// `auth login` no longer takes --host / --name; it authenticates the
	// already-existing active profile (override via the global --profile).
	name, host, err := resolveActiveProfile(f)
	if err != nil {
		return err
	}
	opts.Profile = name
	opts.Host = host
	if svc == nil {
		svc = loginServiceFor(host)
	}

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

	// Interactive: prompt for email + password. svc is always set by now
	// (loginServiceFor returns non-nil because resolveActiveProfile guarantees
	// a non-empty host).
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
	// Switching credential mode: this profile is now an API-key profile, so
	// any leftover JWT refs (from a prior password login) are stale. Set the
	// api-key ref and clear the token/refresh refs; keep Host + existing
	// User/TenantID (merged in saveProfileRef via applyUser).
	mutate := func(prof *config.Profile) {
		prof.APIKeyRef = store.Ref(opts.Profile, "api_key")
		prof.TokenRef = ""
		prof.RefreshRef = ""
		applyUser(prof, user)
	}
	return saveProfileRef(opts, fopts, f, mutate, ModeAPIKey, user)
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
	// Switching to a JWT profile: set token refs and drop any stale api-key
	// ref; keep Host + existing User/TenantID (merged in saveProfileRef via applyUser).
	mutate := func(prof *config.Profile) {
		prof.TokenRef = store.Ref(opts.Profile, "access")
		prof.RefreshRef = store.Ref(opts.Profile, "refresh")
		prof.APIKeyRef = ""
		applyUser(prof, resp.User)
	}
	return saveProfileRef(opts, fopts, f, mutate, ModeBearer, resp.User)
}

// applyUser overwrites prof.User / prof.TenantID only when the server actually
// returned them. A nil user (or empty email) must NOT wipe an existing User
// that was set during `profile add` or a prior login.
func applyUser(prof *config.Profile, user *sdk.AuthUser) {
	if user == nil {
		return
	}
	if user.Email != "" {
		prof.User = user.Email
	}
	if user.TenantID != 0 {
		prof.TenantID = user.TenantID
	}
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

// saveProfileRef MERGES the new credential refs into the EXISTING profile
// record and prints success. The active profile already exists (created via
// `profile add`, carrying Host and possibly User/TenantID); mutate sets only
// the credential refs + any server-returned user, so re-login never clobbers
// the host or wipes an existing user. cfg.CurrentProfile is left untouched -
// `auth login` authenticates the already-active profile, it doesn't switch.
func saveProfileRef(opts *LoginOptions, fopts *cmdutil.FormatOptions, f *cmdutil.Factory, mutate func(*config.Profile), mode string, user *sdk.AuthUser) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.Profile{}
	}
	prof := cfg.Profiles[opts.Profile] // existing record (Host, User, TenantID)
	mutate(&prof)
	cfg.Profiles[opts.Profile] = prof
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}
	if fopts.WantsJSON() {
		// mode reflects the credential actually stored (passed by the caller),
		// not whether the server happened to return a user — an API-key login
		// validates via /auth/me and DOES get a user, so deriving mode from
		// user != nil would mislabel it as bearer.
		result := loginResult{Profile: opts.Profile, Host: opts.Host, Mode: mode}
		if user != nil {
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
