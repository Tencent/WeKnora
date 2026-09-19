package plugin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ControllerFactory builds the per-plugin executor. It is injectable so tests
// can substitute a fake controller without a Docker daemon.
type ControllerFactory func(pluginID string) PluginRuntimeController

// RuntimeAgentServer is the runtime-agent's HTTP server. It listens on a Unix
// socket and exposes exactly three narrow operations: health, start and stop.
// It is the only component that holds Docker privileges, so every request field
// is re-validated and every image is checked against a pinned allowlist rather
// than being trusted from the app.
type RuntimeAgentServer struct {
	runtimeRoot string
	authToken   string
	auditSink   AuditSink
	imagePolicy *ImagePolicy
	instanceID  string
	runtimeGID  int
	factory     ControllerFactory

	mu          sync.Mutex
	controllers map[string]PluginRuntimeController
	requests    map[string]StartPluginRequest
	locks       map[string]*sync.Mutex

	server     *http.Server
	listener   net.Listener
	socketPath string
}

// NewRuntimeAgentServer builds a server backed by local docker controllers.
func NewRuntimeAgentServer(runtimeRoot, authToken string, auditSink AuditSink, imagePolicy *ImagePolicy, instanceID string) *RuntimeAgentServer {
	return newRuntimeAgentServer(runtimeRoot, authToken, auditSink, imagePolicy, instanceID, nil)
}

func newRuntimeAgentServer(runtimeRoot, authToken string, auditSink AuditSink, imagePolicy *ImagePolicy, instanceID string, factory ControllerFactory) *RuntimeAgentServer {
	if auditSink == nil {
		auditSink = LoggerAuditSink{}
	}
	s := &RuntimeAgentServer{
		runtimeRoot: runtimeRoot,
		authToken:   authToken,
		auditSink:   auditSink,
		imagePolicy: imagePolicy,
		instanceID:  instanceID,
		runtimeGID:  DefaultRuntimeGID,
		controllers: make(map[string]PluginRuntimeController),
		requests:    make(map[string]StartPluginRequest),
		locks:       make(map[string]*sync.Mutex),
	}
	if factory == nil {
		factory = s.defaultFactory
	}
	s.factory = factory
	return s
}

func (s *RuntimeAgentServer) defaultFactory(pluginID string) PluginRuntimeController {
	c := NewLocalDockerRuntimeController(filepath.Join(s.runtimeRoot, pluginID), s.auditSink)
	c.InstanceID = s.instanceID
	return c
}

// Serve listens on the given Unix socket. It is fail-closed: an empty auth
// token is rejected so the agent never silently accepts unauthenticated calls.
func (s *RuntimeAgentServer) Serve(socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("runtime agent socket path is empty")
	}
	if s.runtimeRoot == "" {
		return fmt.Errorf("runtime agent runtime root is empty")
	}
	if s.authToken == "" {
		return fmt.Errorf("runtime agent auth token is required (fail-closed)")
	}
	rel, ok := rebasePath(s.runtimeRoot, socketPath)
	if !ok || rel == "." || rel == "" {
		return fmt.Errorf("agent socket path %q must be inside runtime root %q", socketPath, s.runtimeRoot)
	}
	if err := os.MkdirAll(s.runtimeRoot, 0o750); err != nil {
		return fmt.Errorf("create runtime root: %w", err)
	}
	if s.runtimeGID > 0 {
		_ = os.Chown(s.runtimeRoot, -1, s.runtimeGID)
		_ = os.Chmod(s.runtimeRoot, 0o2750)
	}
	// Recover from a previous crash BEFORE the socket becomes visible. Running
	// this synchronously guarantees that once the healthcheck passes and the app
	// starts a fresh container, no background cleanup can later delete it.
	cleanup := NewLocalDockerRuntimeController("", s.auditSink)
	cleanup.InstanceID = s.instanceID
	if err := cleanup.CleanupOrphaned(context.Background()); err != nil {
		return fmt.Errorf("cleanup orphaned containers: %w", err)
	}
	if err := os.RemoveAll(socketPath); err != nil {
		return fmt.Errorf("remove stale agent socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen agent socket: %w", err)
	}
	if s.runtimeGID > 0 {
		_ = os.Chown(socketPath, -1, s.runtimeGID)
		_ = os.Chmod(socketPath, 0o660)
	} else {
		_ = os.Chmod(socketPath, 0o600)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/plugins/start", s.handleStart)
	mux.HandleFunc("/v1/plugins/stop", s.handleStop)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	s.listener = ln
	s.socketPath = socketPath
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			LoggerAuditSink{}.Record(AuditEvent{PluginID: "runtime-agent", Action: "serve", Reason: err.Error()})
		}
	}()
	return nil
}

// Shutdown stops the HTTP server and every running plugin controller.
func (s *RuntimeAgentServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	controllers := make([]PluginRuntimeController, 0, len(s.controllers))
	for id, c := range s.controllers {
		controllers = append(controllers, c)
		delete(s.controllers, id)
		delete(s.requests, id)
	}
	s.mu.Unlock()
	for _, c := range controllers {
		_ = c.Stop(ctx, "")
	}
	var shutdownErr error
	if s.server != nil {
		shutdownErr = s.server.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.socketPath != "" {
		_ = os.Remove(s.socketPath)
	}
	return shutdownErr
}

func (s *RuntimeAgentServer) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	token := r.Header.Get("Authorization")
	if !strings.HasPrefix(token, prefix) {
		return false
	}
	provided := strings.TrimPrefix(token, prefix)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.authToken)) == 1
}

func (s *RuntimeAgentServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *RuntimeAgentServer) lockFor(pluginID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[pluginID] == nil {
		s.locks[pluginID] = &sync.Mutex{}
	}
	return s.locks[pluginID]
}

func (s *RuntimeAgentServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req StartPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateStartRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.imagePolicy.Check(req.PluginID, req.Image); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	lock := s.lockFor(req.PluginID)
	lock.Lock()
	defer lock.Unlock()

	// Idempotent: identical request with a live instance returns its handle.
	if handle := s.currentHandleLocked(req); handle != nil {
		_ = json.NewEncoder(w).Encode(handle)
		return
	}
	// Changed request or first start: stop the old instance first (same
	// deterministic container name would otherwise collide), then start fresh.
	if old := s.takeControllerLocked(req.PluginID); old != nil {
		_ = old.Stop(context.Background(), req.PluginID)
	}
	controller := s.factory(req.PluginID)
	handle, err := controller.Start(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.controllers[req.PluginID] = controller
	s.requests[req.PluginID] = req
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(handle)
}

func (s *RuntimeAgentServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PluginID string `json:"plugin_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.PluginID == "" {
		http.Error(w, "plugin_id is required", http.StatusBadRequest)
		return
	}

	lock := s.lockFor(req.PluginID)
	lock.Lock()
	defer lock.Unlock()

	if controller := s.takeControllerLocked(req.PluginID); controller != nil {
		if err := controller.Stop(r.Context(), req.PluginID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Fallback: even with no in-memory state (agent restarted), force-remove any
	// container this instance still has for the plugin.
	cleanup := NewLocalDockerRuntimeController("", s.auditSink)
	cleanup.InstanceID = s.instanceID
	_ = cleanup.ForceStopByLabels(r.Context(), req.PluginID)
	_, _ = w.Write([]byte("{}"))
}

func (s *RuntimeAgentServer) currentHandleLocked(req StartPluginRequest) *PluginHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	controller, ok := s.controllers[req.PluginID]
	if !ok {
		return nil
	}
	prev, ok := s.requests[req.PluginID]
	if !ok || !reflect.DeepEqual(prev, req) {
		return nil
	}
	if hc, ok := controller.(interface{ CurrentHandle() *PluginHandle }); ok {
		return hc.CurrentHandle()
	}
	return nil
}

func (s *RuntimeAgentServer) takeControllerLocked(pluginID string) PluginRuntimeController {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.controllers[pluginID]
	delete(s.controllers, pluginID)
	delete(s.requests, pluginID)
	return c
}

var pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func validateStartRequest(req StartPluginRequest) error {
	if req.PluginID == "" || !pluginIDPattern.MatchString(req.PluginID) {
		return fmt.Errorf("invalid plugin_id %q", req.PluginID)
	}
	if req.Image == "" {
		return fmt.Errorf("plugin %q has no image", req.PluginID)
	}
	switch req.ExtensionType {
	case ExtensionDataSource, ExtensionParser, ExtensionSearch, ExtensionModel, ExtensionRetriever:
	default:
		return fmt.Errorf("unsupported extension_type %q", req.ExtensionType)
	}
	if req.ProtocolVersion != "" && req.ProtocolVersion != ProtocolVersionV1 {
		return fmt.Errorf("unsupported protocol_version %q", req.ProtocolVersion)
	}
	switch req.NetworkPolicy {
	case NetworkNone:
	case NetworkAllowlist:
		if len(req.AllowedDestinations) == 0 {
			return fmt.Errorf("network allowlist requires allowed_destinations")
		}
	default:
		return fmt.Errorf("unsupported network_policy %q", req.NetworkPolicy)
	}
	return nil
}

// rebasePath returns path relative to runtimeRoot, rejecting any path that is
// not strictly inside it (no "..", no prefix-collision like /run/root-evil).
func rebasePath(runtimeRoot, path string) (string, bool) {
	cleanRoot := filepath.Clean(runtimeRoot)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
