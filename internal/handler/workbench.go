package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type workbenchService interface {
	Enabled() bool
	Authorize(context.Context, string) (*types.Session, error)
	Status(context.Context, string) (*service.WorkbenchStatus, error)
	Bind(context.Context, string, string) (*service.WorkbenchStatus, error)
	Files(context.Context, string, sandbox.WorkbenchFileRequest) (*sandbox.WorkbenchFileResult, error)
	Audit(context.Context, string, uint64, int) ([]*types.AuditLog, error)
	IssueTicket(context.Context, string, string, string) (*service.WorkbenchTicket, error)
	ConsumeTicket(context.Context, string, string) (service.WorkbenchIdentity, error)
	AcquireConsole(context.Context, service.WorkbenchIdentity) (*service.WorkbenchLease, error)
	OpenTerminal(context.Context, string, sandbox.CommandTerminalRequest) (*service.WorkbenchExecution, error)
}

// WorkbenchHandler serves authorized workspace files, commands and audit records.
type WorkbenchHandler struct {
	service         workbenchService
	origins         map[string]bool
	unauthenticated chan struct{}
	authTimeout     time.Duration
	recheckInterval time.Duration
	writeTimeout    time.Duration
	consoleMu       sync.Mutex
	consoles        map[*workbenchConsole]struct{}
	sockets         map[io.Closer]struct{}
	consoleWG       sync.WaitGroup
	socketWG        sync.WaitGroup
	shuttingDown    bool
}

// NewWorkbenchHandler validates the origin allowlist when workbench is enabled.
func NewWorkbenchHandler(s *service.WorkbenchService) (*WorkbenchHandler, error) {
	h := &WorkbenchHandler{
		service: s, origins: make(map[string]bool), unauthenticated: make(chan struct{}, 64),
		authTimeout: 5 * time.Second, recheckInterval: 5 * time.Second, writeTimeout: 5 * time.Second,
		consoles: make(map[*workbenchConsole]struct{}), sockets: make(map[io.Closer]struct{}),
	}
	if s == nil || !s.Enabled() {
		return h, nil
	}
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_SANDBOX_WORKBENCH_ORIGINS")); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			origin, err := workbenchOrigin(strings.TrimSpace(value))
			if err != nil {
				return nil, errors.New("invalid WEKNORA_SANDBOX_WORKBENCH_ORIGINS")
			}
			h.origins[origin] = true
		}
	}
	return h, nil
}

func (h *WorkbenchHandler) registerSocket(socket io.Closer) bool {
	h.consoleMu.Lock()
	defer h.consoleMu.Unlock()
	if h.shuttingDown {
		return false
	}
	h.sockets[socket] = struct{}{}
	h.socketWG.Add(1)
	return true
}

func (h *WorkbenchHandler) unregisterSocket(socket io.Closer) {
	h.consoleMu.Lock()
	delete(h.sockets, socket)
	h.consoleMu.Unlock()
	h.socketWG.Done()
}

func (h *WorkbenchHandler) registerConsole(console *workbenchConsole) bool {
	h.consoleMu.Lock()
	defer h.consoleMu.Unlock()
	if h.shuttingDown {
		return false
	}
	h.consoles[console] = struct{}{}
	h.consoleWG.Add(1)
	return true
}

func (h *WorkbenchHandler) unregisterConsole(console *workbenchConsole) {
	h.consoleMu.Lock()
	delete(h.consoles, console)
	h.consoleMu.Unlock()
	h.consoleWG.Done()
}

func (h *WorkbenchHandler) isShuttingDown() bool {
	h.consoleMu.Lock()
	defer h.consoleMu.Unlock()
	return h.shuttingDown
}

// Shutdown prevents new authenticated consoles, cancels active ones and waits
// for command cleanup, completion auditing and lease release.
func (h *WorkbenchHandler) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.consoleMu.Lock()
	h.shuttingDown = true
	sockets := make([]io.Closer, 0, len(h.sockets))
	for socket := range h.sockets {
		sockets = append(sockets, socket)
	}
	for console := range h.consoles {
		console.cancel()
	}
	h.consoleMu.Unlock()
	for _, socket := range sockets {
		_ = socket.Close()
	}

	done := make(chan struct{})
	go func() {
		h.consoleWG.Wait()
		h.socketWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func workbenchOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\t\r\n ,\\#?") {
		return "", service.ErrWorkbenchInvalid
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func (h *WorkbenchHandler) requestOrigin(r *http.Request, requireHeader bool) (string, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Forwarded headers are not an origin authority. Reverse-proxy deployments
	// with a different public origin configure the explicit allowlist.
	same, err := workbenchOrigin(scheme + "://" + r.Host)
	if err != nil {
		return "", err
	}
	values := r.Header.Values("Origin")
	if len(values) == 0 && !requireHeader {
		return same, nil
	}
	if len(values) != 1 {
		return "", service.ErrWorkbenchDenied
	}
	origin, err := workbenchOrigin(values[0])
	if err != nil || (origin != same && !h.origins[origin]) {
		return "", service.ErrWorkbenchDenied
	}
	return origin, nil
}

func workbenchSessionID(c *gin.Context) string {
	if id := c.Param("session_id"); id != "" {
		return id
	}
	return c.Param("id")
}

func workbenchJSON(c *gin.Context, value any, err error) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": value})
}

func workbenchError(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrWorkbenchDisabled):
		return 404, "disabled", "Workbench is disabled"
	case errors.Is(err, service.ErrWorkbenchDenied):
		return 403, "forbidden", "Active web user and workspace membership required"
	case errors.Is(err, service.ErrWorkbenchSession):
		return 404, "not_found", "Session not found"
	case errors.Is(err, service.ErrWorkbenchPolicy):
		return 403, "policy_disabled", "Workspace scripts are disabled"
	case errors.Is(err, service.ErrWorkbenchTicket):
		return 401, "invalid_ticket", "Invalid or expired terminal ticket"
	case errors.Is(err, service.ErrWorkbenchInvalid):
		return 400, "invalid_request", "Invalid workbench request"
	case errors.Is(err, sandbox.ErrWorkbenchPath):
		return 400, "invalid_path", "Invalid workbench path or file type"
	case errors.Is(err, sandbox.ErrWorkbenchNotFound):
		return 404, "not_found", "File not found"
	case errors.Is(err, sandbox.ErrWorkbenchConflict):
		return 409, "conflict", "Destination exists or path changed"
	case errors.Is(err, sandbox.ErrWorkbenchTooLarge):
		return 413, "too_large", "Workbench size limit exceeded"
	case errors.Is(err, service.ErrWorkbenchUnbound):
		return 409, "unbound", "Bind a sandbox configuration first"
	case errors.Is(err, service.ErrWorkbenchBusy):
		return 409, "console_busy", "Session console is already in use"
	case errors.Is(err, service.ErrWorkbenchCapability):
		return 409, "capability_unavailable", "Sandbox capability unavailable"
	case errors.Is(err, service.ErrWorkbenchAudit):
		return 503, "audit_unavailable", "Durable audit write failed; outcome may be unknown"
	default:
		return 503, "unavailable", "Workbench operation unavailable"
	}
}

func workbenchHTTPError(c *gin.Context, err error) {
	status, _, message := workbenchError(err)
	code := apperrors.ErrServiceUnavailable
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		code = apperrors.ErrBadRequest
	case http.StatusUnauthorized:
		code = apperrors.ErrUnauthorized
	case http.StatusForbidden:
		code = apperrors.ErrForbidden
	case http.StatusNotFound:
		code = apperrors.ErrNotFound
	case http.StatusConflict:
		code = apperrors.ErrConflict
	}
	c.Header("Cache-Control", "no-store")
	_ = c.Error(&apperrors.AppError{Code: code, Message: message, HTTPCode: status})
	c.Abort()
}

func decodeWorkbenchJSON(c *gin.Context, value any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.WorkbenchMaxFrameBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return service.ErrWorkbenchInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return service.ErrWorkbenchInvalid
	}
	return nil
}

// Status reads the session binding and advertised capabilities.
func (h *WorkbenchHandler) Status(c *gin.Context) {
	data, err := h.service.Status(c.Request.Context(), workbenchSessionID(c))
	workbenchJSON(c, data, err)
}

// Bind initializes the workspace on the requested sandbox configuration.
func (h *WorkbenchHandler) Bind(c *gin.Context) {
	var request struct {
		ConfigID string `json:"config_id"`
	}
	if err := decodeWorkbenchJSON(c, &request); err != nil {
		workbenchHTTPError(c, err)
		return
	}
	data, err := h.service.Bind(c.Request.Context(), workbenchSessionID(c), request.ConfigID)
	workbenchJSON(c, data, err)
}

// Ticket issues a single-use, origin-bound command console credential.
func (h *WorkbenchHandler) Ticket(c *gin.Context) {
	if !h.Enabled() {
		workbenchHTTPError(c, service.ErrWorkbenchDisabled)
		return
	}
	origin, err := h.requestOrigin(c.Request, false)
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		workbenchHTTPError(c, service.ErrWorkbenchDenied)
		return
	}
	data, err := h.service.IssueTicket(c.Request.Context(), workbenchSessionID(c), origin, parts[1])
	workbenchJSON(c, data, err)
}

// ListFiles enumerates one directory within the session output root.
func (h *WorkbenchHandler) ListFiles(c *gin.Context) {
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), sandbox.WorkbenchFileRequest{
		Operation: "list", Path: c.Query("path"),
	})
	workbenchJSON(c, data, err)
}

// Download serves an output file as an attachment, never as active content.
func (h *WorkbenchHandler) Download(c *gin.Context) {
	filePath := c.Query("path")
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), sandbox.WorkbenchFileRequest{
		Operation: "read", Path: filePath,
	})
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filePath)})
	c.Header("Content-Disposition", disposition)
	c.Data(http.StatusOK, "application/octet-stream", data.Content)
}

// Upload validates a bounded multipart request before writing an output file.
func (h *WorkbenchHandler) Upload(c *gin.Context) {
	if _, err := h.service.Authorize(c.Request.Context(), workbenchSessionID(c)); err != nil {
		workbenchHTTPError(c, err)
		return
	}
	request, err := readWorkbenchUpload(c)
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), request)
	workbenchJSON(c, data, err)
}

// Read multipart parts directly: multipart.FileHeader.Filename has already
// stripped directory components, which would erase a malicious traversal.
func readWorkbenchUpload(c *gin.Context) (sandbox.WorkbenchFileRequest, error) {
	fail := func(err error) (sandbox.WorkbenchFileRequest, error) { return sandbox.WorkbenchFileRequest{}, err }
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.WorkbenchMaxFileBytes+(64<<10))
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return fail(service.ErrWorkbenchInvalid)
	}
	var destination, filename string
	var content []byte
	seen := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(service.ErrWorkbenchInvalid)
		}
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			return fail(service.ErrWorkbenchInvalid)
		}
		name := params["name"]
		if seen[name] {
			return fail(service.ErrWorkbenchInvalid)
		}
		seen[name] = true
		switch name {
		case "path":
			if _, file := params["filename"]; file {
				return fail(service.ErrWorkbenchInvalid)
			}
			value, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil || len(value) > 4096 {
				return fail(service.ErrWorkbenchInvalid)
			}
			destination = string(value)
		case "file":
			filename = params["filename"]
			if service.ValidateWorkbenchPath(filename, false) != nil || strings.Contains(filename, "/") {
				return fail(service.ErrWorkbenchInvalid)
			}
			content, err = io.ReadAll(io.LimitReader(part, service.WorkbenchMaxFileBytes+1))
			if err != nil {
				return fail(service.ErrWorkbenchInvalid)
			}
			if len(content) > service.WorkbenchMaxFileBytes {
				return fail(sandbox.ErrWorkbenchTooLarge)
			}
		default:
			return fail(service.ErrWorkbenchInvalid)
		}
		_ = part.Close()
	}
	if !seen["file"] || !seen["path"] || service.ValidateWorkbenchPath(destination, false) != nil {
		return fail(service.ErrWorkbenchInvalid)
	}
	return sandbox.WorkbenchFileRequest{Operation: "write", Path: destination, Content: content}, nil
}

// Directory creates a directory inside the session output root.
func (h *WorkbenchHandler) Directory(c *gin.Context) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeWorkbenchJSON(c, &request); err != nil {
		workbenchHTTPError(c, err)
		return
	}
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), sandbox.WorkbenchFileRequest{
		Operation: "mkdir", Path: request.Path,
	})
	workbenchJSON(c, data, err)
}

// Rename moves an output path without overwriting an existing destination.
func (h *WorkbenchHandler) Rename(c *gin.Context) {
	var request struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}
	if err := decodeWorkbenchJSON(c, &request); err != nil {
		workbenchHTTPError(c, err)
		return
	}
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), sandbox.WorkbenchFileRequest{
		Operation: "rename", Path: request.Path, NewPath: request.NewPath,
	})
	workbenchJSON(c, data, err)
}

// Remove deletes an output file or empty directory.
func (h *WorkbenchHandler) Remove(c *gin.Context) {
	data, err := h.service.Files(c.Request.Context(), workbenchSessionID(c), sandbox.WorkbenchFileRequest{
		Operation: "remove", Path: c.Query("path"),
	})
	workbenchJSON(c, data, err)
}

// Audit lists one cursor-paginated page for this session and user.
func (h *WorkbenchHandler) Audit(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	afterID, err := strconv.ParseUint(c.DefaultQuery("after_id", "0"), 10, 64)
	if err != nil {
		workbenchHTTPError(c, service.ErrWorkbenchInvalid)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 {
		workbenchHTTPError(c, service.ErrWorkbenchInvalid)
		return
	}
	data, err := h.service.Audit(c.Request.Context(), workbenchSessionID(c), afterID, limit)
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	var nextCursor uint64
	if n := len(data); n > 0 {
		nextCursor = data[n-1].ID
	}
	c.JSON(http.StatusOK, auditLogListResponse{Success: true, Data: data, NextCursor: nextCursor})
}
