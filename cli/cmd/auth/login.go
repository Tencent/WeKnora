package auth

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// LoginOptions is the configuration captured from flags + prompts.
type LoginOptions struct {
	Host        string // --host
	Context     string // --name: context name to write into config.yaml
	//                                (was --context in v0.0; renamed v0.1 to
	//                                avoid shadowing the new global --context
	//                                single-shot override flag)
	WithToken   bool   // --with-token (read api key from stdin instead of prompting password)
	APIKey      string // populated by --with-token from stdin
	Email       string
	Password    string
	JSONOut     bool
	StdinReader io.Reader // override for tests
}

// LoginService is the narrow SDK surface auth login depends on.
// *sdk.Client satisfies it implicitly via the new Login(ctx, LoginRequest)
// signature added in client/auth.go.
type LoginService interface {
	Login(ctx context.Context, req sdk.LoginRequest) (*sdk.LoginResponse, error)
}

// NewCmdLogin builds the `weknora auth login` command. runF is the testable
// entrypoint (left nil for production; see cli/cmd/auth/login_test.go).
func NewCmdLogin(f *cmdutil.Factory, runF func(context.Context, *LoginOptions, *cmdutil.Factory, LoginService) error) *cobra.Command {
	opts := &LoginOptions{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a WeKnora server and persist credentials",
		Long: `Log in by email + password (interactive prompt) or pipe an API key with --with-token.

Credentials are persisted to the OS keyring when available; otherwise to a
0600 file under $XDG_CONFIG_HOME/weknora/secrets. The named context becomes
the current_context in ~/.config/weknora/config.yaml.`,
		RunE: func(c *cobra.Command, args []string) error {
			run := runF
			if run == nil {
				run = runLogin
			}
			svc := loginServiceFor(opts.Host)
			if opts.StdinReader == nil {
				opts.StdinReader = iostreams.IO.In
			}
			return run(c.Context(), opts, f, svc)
		},
	}
	cmd.Flags().StringVar(&opts.Host, "host", "", "WeKnora server URL, e.g. https://kb.example.com")
	cmd.Flags().StringVar(&opts.Context, "name", "default", "Context name to register in config.yaml")
	cmd.Flags().BoolVar(&opts.WithToken, "with-token", false, "Read an API key from stdin instead of prompting for password")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	cmdutil.MustRequireFlag(cmd, "host")
	return cmd
}

// loginServiceFor returns a fresh SDK client targeting host. login.go cannot
// reuse Factory.Client because that closure requires an existing context.
func loginServiceFor(host string) LoginService {
	if host == "" {
		return nil
	}
	return sdk.NewClient(host)
}

func runLogin(ctx context.Context, opts *LoginOptions, f *cmdutil.Factory, svc LoginService) error {
	if err := validateHost(opts.Host); err != nil {
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
		return persistAPIKey(opts, f)
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
		return cmdutil.Wrapf(cmdutil.CodeAuthBadCredential, err, "login")
	}
	if !resp.Success || resp.Token == "" {
		return cmdutil.NewError(cmdutil.CodeAuthBadCredential, fmt.Sprintf("login refused: %s", resp.Message))
	}

	return persistJWT(opts, f, resp)
}

// persistAPIKey saves the --with-token API key and writes the context.
func persistAPIKey(opts *LoginOptions, f *cmdutil.Factory) error {
	store, err := f.Secrets()
	if err != nil {
		return err
	}
	if err := store.Set(opts.Context, "api_key", opts.APIKey); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save api key")
	}
	return saveContextRef(opts, f, &config.Context{
		Host:      opts.Host,
		APIKeyRef: store.Ref(opts.Context, "api_key"),
	}, nil)
}

// persistJWT saves access + refresh tokens and writes the context.
func persistJWT(opts *LoginOptions, f *cmdutil.Factory, resp *sdk.LoginResponse) error {
	store, err := f.Secrets()
	if err != nil {
		return err
	}
	if err := store.Set(opts.Context, "access", resp.Token); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save access token")
	}
	if resp.RefreshToken != "" {
		if err := store.Set(opts.Context, "refresh", resp.RefreshToken); err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalKeychainDenied, err, "save refresh token")
		}
	}
	c := &config.Context{
		Host:       opts.Host,
		TokenRef:   store.Ref(opts.Context, "access"),
		RefreshRef: store.Ref(opts.Context, "refresh"),
	}
	if resp.User != nil {
		c.User = resp.User.Email
		c.TenantID = resp.User.TenantID
	}
	return saveContextRef(opts, f, c, resp.User)
}

// loginResult is the typed payload emitted by `--json`. mode is derived from
// whether the server returned a user (password flow) vs API-key flow.
type loginResult struct {
	Context  string `json:"context"`
	Host     string `json:"host"`
	Mode     string `json:"mode"` // "password" or "api-key"
	User     string `json:"user,omitempty"`
	TenantID uint64 `json:"tenant_id,omitempty"`
}

// saveContextRef writes the context to config.yaml and prints success.
func saveContextRef(opts *LoginOptions, f *cmdutil.Factory, ctx *config.Context, user *sdk.AuthUser) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]config.Context{}
	}
	cfg.Contexts[opts.Context] = *ctx
	cfg.CurrentContext = opts.Context
	if err := config.Save(cfg); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "save config")
	}
	if opts.JSONOut {
		result := loginResult{Context: opts.Context, Host: opts.Host, Mode: "api-key"}
		if user != nil {
			result.Mode = "password"
			result.User = user.Email
			result.TenantID = user.TenantID
		}
		return cmdutil.NewJSONExporter().Write(iostreams.IO.Out, format.Success(result, &format.Meta{
			Context:  opts.Context,
			TenantID: ctx.TenantID,
		}))
	}
	who := opts.Context
	if user != nil {
		who = user.Email
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Logged in to %s as %s (context=%s)\n", opts.Host, who, opts.Context)
	return nil
}

// validateHost rejects empty / non-http URLs early so we surface a clean
// flag error instead of failing inside the SDK transport.
func validateHost(host string) error {
	if host == "" {
		return cmdutil.NewError(cmdutil.CodeInputMissingFlag, "--host is required")
	}
	u, err := url.Parse(host)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("--host must be http(s) URL, got %q", host))
	}
	return nil
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
