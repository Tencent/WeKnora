// Package sandbox: envd protocol compatibility for the E2B data plane.
//
// The sandbox-side daemon (envd) authenticates every data-plane call with HTTP
// Basic auth carrying the sandbox account name, and accepts file uploads only
// as multipart/form-data. github.com/matiasinsaurralde/go-e2b predates both:
// it sends the account in an X-User-ID header and POSTs file contents as a raw
// octet-stream body.
//
// E2B Cloud's own gateway is lenient enough to hide the difference, so the gap
// only surfaces against other implementations of the protocol — the very
// backends WeKnora wants to support without carrying one adapter per vendor.
// Rather than fork the SDK, this transport rewrites the two data-plane details
// on the way out:
//
//   - it adds "Authorization: Basic base64(user:)" when the request carries no
//     credentials of its own;
//   - it re-wraps a non-multipart /files upload as multipart/form-data.
//
// Requests are recognised by path, not by host, so the shim works whether the
// data plane is reached directly or through a gateway (gateway_transport.go).
// Control-plane calls never use these paths and pass through untouched.
package sandbox

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
)

// envdFilesRoute is envd's upload/download endpoint.
const envdFilesRoute = "/files"

// envdUploadFormField is the multipart field name envd reads the payload from.
const envdUploadFormField = "file"

// NewEnvdCompatTransport wraps next so E2B data-plane requests satisfy the
// current envd contract. A blank user leaves authentication untouched.
func NewEnvdCompatTransport(next http.RoundTripper, user string) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &envdCompatTransport{next: next, user: strings.TrimSpace(user)}
}

type envdCompatTransport struct {
	next http.RoundTripper
	user string
}

func (t *envdCompatTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isEnvdDataPlaneRequest(req) {
		return t.next.RoundTrip(req)
	}
	rewritten := req.Clone(req.Context())
	if t.user != "" && rewritten.Header.Get("Authorization") == "" {
		rewritten.Header.Set("Authorization", basicAuthorizationFor(t.user))
	}
	if err := rewriteEnvdUpload(rewritten); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(rewritten)
}

func (t *envdCompatTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// isEnvdDataPlaneRequest reports whether req addresses envd rather than the
// control plane. envd serves /files plus the ConnectRPC services generated
// from its proto package.
func isEnvdDataPlaneRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	requestPath := req.URL.Path
	if requestPath == envdFilesRoute {
		return true
	}
	return strings.HasPrefix(requestPath, "/filesystem.Filesystem/") ||
		strings.HasPrefix(requestPath, "/process.Process/")
}

// basicAuthorizationFor builds envd's credential: the account name as the
// username with an empty password.
func basicAuthorizationFor(user string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"))
}

// rewriteEnvdUpload converts a raw-body upload into the multipart form envd
// expects. Uploads that already are multipart, and every non-upload request,
// are left alone.
func rewriteEnvdUpload(req *http.Request) error {
	if req.Method != http.MethodPost || req.URL.Path != envdFilesRoute {
		return nil
	}
	if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/") {
		return nil
	}
	if req.Body == nil {
		return nil
	}
	payload, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return fmt.Errorf("sandbox: read envd upload body: %w", err)
	}

	filename := path.Base(strings.TrimSpace(req.URL.Query().Get("path")))
	if filename == "" || filename == "." || filename == "/" {
		filename = envdUploadFormField
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile(envdUploadFormField, filename)
	if err != nil {
		return fmt.Errorf("sandbox: build envd upload form: %w", err)
	}
	if _, err := part.Write(payload); err != nil {
		return fmt.Errorf("sandbox: write envd upload form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("sandbox: close envd upload form: %w", err)
	}

	body := form.Bytes()
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = int64(len(body))
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
}
