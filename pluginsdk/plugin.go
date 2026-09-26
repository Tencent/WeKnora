// Package pluginsdk builds WeKnora code plugins in Go. A plugin registers what
// it contributes and calls Serve; the SDK speaks the extension protocol
// (package pluginapi): the handshake with the plugin host, authentication,
// routing, streaming and error encoding.
//
//	func main() {
//		p := pluginsdk.New(pluginsdk.Info{ID: "acme.rss", Version: "1.0.0"})
//		p.Connector("rss", &rssConnector{})
//		if err := p.Serve(); err != nil {
//			log.Fatal(err)
//		}
//	}
//
// Started by the WeKnora plugin host, the plugin listens where the host says
// and accepts only the host's token. Run on its own (a remote plugin), it
// listens on WEKNORA_PLUGIN_ADDR and verifies request signatures with
// WEKNORA_PLUGIN_SECRET.
package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Info identifies the plugin; it must match plugin.yaml.
type Info struct {
	ID      string
	Version string
}

// Call is one request's context and configuration. Configuration secrets are
// already decrypted; do not keep them beyond the call.
type Call struct {
	pluginapi.Context
	Config pluginapi.Config
}

// DecodeInstance decodes the instance configuration into v.
func (c *Call) DecodeInstance(v any) error {
	return remarshal(c.Config.Instance, v)
}

// ConfigValidator checks the plugin's system and tenant configuration.
type ConfigValidator func(ctx context.Context, call *Call) error

// Plugin is a plugin under construction; register contributions, then Serve.
type Plugin struct {
	info       Info
	webSearch  map[string]WebSearcher
	connectors map[string]Connector
	parsers    map[string]Parser
	ui         UIHandler
	events     EventHandler
	webhooks   map[string]WebhookHandler
	options    map[string]OptionsHandler
	validate   ConfigValidator
	logger     *slog.Logger
	// ShutdownTimeout bounds how long Serve waits for calls in flight after
	// a termination signal.
	ShutdownTimeout time.Duration
}

// New starts a plugin.
func New(info Info) *Plugin {
	return &Plugin{
		info:            info,
		webSearch:       map[string]WebSearcher{},
		connectors:      map[string]Connector{},
		parsers:         map[string]Parser{},
		webhooks:        map[string]WebhookHandler{},
		options:         map[string]OptionsHandler{},
		logger:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
		ShutdownTimeout: 60 * time.Second,
	}
}

// Logger writes to stderr, which the plugin host forwards to WeKnora's logs.
func (p *Plugin) Logger() *slog.Logger { return p.logger }

// ValidateConfig registers the check behind POST /v1/config/validate.
func (p *Plugin) ValidateConfig(fn ConfigValidator) { p.validate = fn }

// Manifest is what the plugin reports at GET /v1/manifest.
func (p *Plugin) Manifest() pluginapi.Manifest {
	m := pluginapi.Manifest{
		ID: p.info.ID, Version: p.info.Version, APIVersion: pluginapi.APIVersion,
		Contributes: map[string][]string{},
	}
	add := func(point string, ids []string) {
		if len(ids) > 0 {
			sort.Strings(ids)
			m.Contributes[point] = ids
		}
	}
	add("webSearch", keys(p.webSearch))
	add("connectors", keys(p.connectors))
	add("parsers", keys(p.parsers))
	add("webhooks", keys(p.webhooks))
	add("options", keys(p.options))
	if p.events != nil {
		m.Contributes["events"] = []string{"handler"}
	}
	if p.ui != nil {
		m.Contributes["ui"] = []string{"request"}
	}
	return m
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Handler serves the protocol. Serve wraps it with authentication; tests
// and custom servers can use it directly.
func (p *Plugin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/manifest", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, p.Manifest())
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, pluginapi.Health{Status: "ok"})
	})
	mux.HandleFunc(
		"POST /v1/config/validate",
		p.unary(func(ctx context.Context, call *Call, _ json.RawMessage) (any, error) {
			if p.validate != nil {
				if err := p.validate(ctx, call); err != nil {
					return nil, err
				}
			}
			return struct{}{}, nil
		}),
	)
	p.routeWebSearch(mux)
	p.routeConnectors(mux)
	p.routeParsers(mux)
	p.routeUI(mux)
	p.routeEvents(mux)
	p.routeOptions(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no endpoint %s %s", r.Method, r.URL.Path))
	})
	return recoverer(p.logger, mux)
}

// unaryFunc answers one call with one output.
type unaryFunc func(ctx context.Context, call *Call, input json.RawMessage) (any, error)

func (p *Plugin) unary(fn unaryFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env, err := readEnvelope(r)
		if err != nil {
			writeError(w, err)
			return
		}
		ctx, cancel := callContext(r.Context(), env)
		defer cancel()
		out, err := fn(ctx, &Call{Context: env.Context, Config: env.Config}, env.Input)
		if err != nil {
			writeError(w, err)
			return
		}
		raw, err := json.Marshal(out)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, pluginapi.Output{Output: raw})
	}
}

func readEnvelope(r *http.Request) (*pluginapi.Envelope, error) {
	var env pluginapi.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		return nil, pluginapi.Errorf(pluginapi.CodeBadRequest, "decode envelope: %v", err)
	}
	return &env, nil
}

// callContext applies the envelope deadline.
func callContext(parent context.Context, env *pluginapi.Envelope) (context.Context, context.CancelFunc) {
	if env.Context.Deadline != nil {
		return context.WithDeadline(parent, *env.Context.Deadline)
	}
	return context.WithCancel(parent)
}

func decodeInput[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, pluginapi.Errorf(pluginapi.CodeBadRequest, "decode input: %v", err)
	}
	return v, nil
}

func remarshal(from, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// toError maps any error to a protocol error.
func toError(err error) *pluginapi.Error {
	if e, ok := pluginapi.AsError(err); ok {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return pluginapi.Errorf(pluginapi.CodeUnavailable, "deadline exceeded")
	}
	return &pluginapi.Error{Code: pluginapi.CodeInternal, Message: err.Error()}
}

func writeError(w http.ResponseWriter, err error) {
	e := toError(err)
	writeJSON(w, e.Code.HTTPStatus(), pluginapi.ErrorBody{Error: *e})
}

func recoverer(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				logger.Error("plugin panicked", "path", r.URL.Path, "panic", v)
				writeError(w, fmt.Errorf("plugin panicked: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Serve runs the plugin until SIGTERM or SIGINT, then drains calls in
// flight. Started by the plugin host it listens where the host says and
// prints the handshake; otherwise it serves as a remote plugin.
func (p *Plugin) Serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	return p.ServeContext(ctx)
}

// ServeContext is Serve with a caller-controlled lifetime.
func (p *Plugin) ServeContext(ctx context.Context) error {
	ln, auth, handshake, err := p.listen()
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: auth(p.Handler()), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	if handshake != nil {
		// The host waits for this exact line; everything else on stdout is
		// treated as log output.
		_, _ = fmt.Fprintln(os.Stdout, handshake.String())
	} else {
		p.logger.Info("plugin serving", "addr", ln.Addr().String())
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), p.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdown)
}

type middleware func(http.Handler) http.Handler

// listen picks host mode (socket + token from the environment) or remote
// mode (address + signing secret).
func (p *Plugin) listen() (net.Listener, middleware, *pluginapi.Handshake, error) {
	if addr := os.Getenv(pluginapi.EnvSocket); addr != "" {
		network := os.Getenv(pluginapi.EnvNetwork)
		if network == "" {
			network = "unix"
		}
		token := os.Getenv(pluginapi.EnvToken)
		if token == "" {
			return nil, nil, nil, fmt.Errorf("%s is set without %s", pluginapi.EnvSocket, pluginapi.EnvToken)
		}
		if network == "unix" {
			_ = os.Remove(addr)
		}
		ln, err := net.Listen(network, addr)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("listen %s %s: %w", network, addr, err)
		}
		hs := &pluginapi.Handshake{Protocol: pluginapi.ProtocolVersion, Network: network, Address: ln.Addr().String()}
		return ln, bearerAuth(token), hs, nil
	}
	addr := os.Getenv(pluginapi.EnvAddr)
	if addr == "" {
		addr = ":8080"
	}
	secret := os.Getenv(pluginapi.EnvSecret)
	if secret == "" {
		return nil, nil, nil, fmt.Errorf(
			"a remote plugin needs %s (the secret shown when it was registered)",
			pluginapi.EnvSecret,
		)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, nil, err
	}
	return ln, signatureAuth([]byte(secret)), nil, nil
}

func bearerAuth(token string) middleware {
	want := "Bearer " + token
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != want {
				writeError(w, pluginapi.Errorf(pluginapi.CodeUnauthorized, "missing or wrong host token"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxBody bounds a signed request body read into memory for verification.
const maxBody = 32 << 20

func signatureAuth(secret []byte) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
			if err != nil || len(body) > maxBody {
				writeError(w, pluginapi.Errorf(pluginapi.CodeBadRequest, "request body unreadable or too large"))
				return
			}
			err = pluginapi.VerifySignature(secret, r.Header.Get(pluginapi.TimestampHeader),
				r.Header.Get(pluginapi.SignatureHeader), body, time.Now())
			if err != nil {
				writeError(w, pluginapi.Errorf(pluginapi.CodeUnauthorized, "%v", err))
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			next.ServeHTTP(w, r)
		})
	}
}
