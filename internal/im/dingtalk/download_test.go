package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/im"
)

func useTestHTTPClient(t *testing.T) {
	t.Helper()
	original := httpClient
	httpClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { httpClient = original })
}

// rejectFileFetchTransport lets OpenAPI calls pass through to a plain
// transport but fails any request whose path ends in /temp/file.
type rejectFileFetchTransport struct{ next http.RoundTripper }

func (t rejectFileFetchTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/temp/file") {
		return nil, fmt.Errorf("shared client must not fetch the download url")
	}
	return t.next.RoundTrip(r)
}

// useRejectingFileFetchHTTPClient replaces the shared httpClient with one
// that serves OpenAPI paths but fails any request for the file path. A
// successful download in TestDownloadFile_URLRewrite then proves the file
// GET bypassed the shared client and used the adapter's trusted download
// client.
func useRejectingFileFetchHTTPClient(t *testing.T) {
	t.Helper()
	original := httpClient
	httpClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: rejectFileFetchTransport{next: &http.Transport{}},
	}
	t.Cleanup(func() { httpClient = original })
}

// TestDownloadFile_EndToEnd drives the full DownloadFile orchestration against a
// fake DingTalk OpenAPI: access-token fetch → messageFiles/download (downloadCode
// → temporary downloadUrl) → GET the temp URL for the bytes. This covers the HTTP
// path that the pure-function unit tests deliberately skip (issue #1771).
func TestDownloadFile_EndToEnd(t *testing.T) {
	useTestHTTPClient(t)
	fileBytes := []byte("%PDF-1.7 fake product spec bytes")

	var downloadReq map[string]string
	var tokenSeen string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "tok-abc",
				"expireIn":    7200,
			})
		case "/v1.0/robot/messageFiles/download":
			tokenSeen = r.Header.Get("x-acs-dingtalk-access-token")
			_ = json.NewDecoder(r.Body).Decode(&downloadReq)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"downloadUrl": srv.URL + "/temp/file",
			})
		case "/temp/file":
			_, _ = w.Write(fileBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orig := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = orig }()

	origValidate := validateFileDownloadURL
	validateFileDownloadURL = func(string) error { return nil }
	defer func() { validateFileDownloadURL = origValidate }()

	a := &Adapter{clientID: "cid", clientSecret: "sec"}
	msg := &im.IncomingMessage{
		MessageType: im.MessageTypeFile,
		FileKey:     "DL-CODE",
		FileName:    "spec.pdf",
		Extra:       map[string]string{"robot_code": "rc-1"},
	}

	reader, name, err := a.DownloadFile(context.Background(), msg)
	if err != nil {
		t.Fatalf("DownloadFile error: %v", err)
	}
	defer reader.Close()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(fileBytes) {
		t.Errorf("downloaded bytes = %q, want %q", got, fileBytes)
	}
	if name != "spec.pdf" {
		t.Errorf("resolved name = %q, want %q", name, "spec.pdf")
	}
	if tokenSeen != "tok-abc" {
		t.Errorf("download request auth header = %q, want %q", tokenSeen, "tok-abc")
	}
	if downloadReq["robotCode"] != "rc-1" {
		t.Errorf("robotCode sent = %q, want %q", downloadReq["robotCode"], "rc-1")
	}
	if downloadReq["downloadCode"] != "DL-CODE" {
		t.Errorf("downloadCode sent = %q, want %q", downloadReq["downloadCode"], "DL-CODE")
	}
}

// TestDownloadFile_TempURLError verifies a non-200 from the temporary download
// URL surfaces as an error rather than silently returning empty content.
func TestDownloadFile_TempURLError(t *testing.T) {
	useTestHTTPClient(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "tok", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]string{"downloadUrl": srv.URL + "/temp/gone"})
		case "/temp/gone":
			http.Error(w, "expired", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orig := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = orig }()

	origValidate := validateFileDownloadURL
	validateFileDownloadURL = func(string) error { return nil }
	defer func() { validateFileDownloadURL = origValidate }()

	a := &Adapter{clientID: "cid", clientSecret: "sec"}
	msg := &im.IncomingMessage{FileKey: "DL-CODE", FileName: "x.pdf", Extra: map[string]string{"robot_code": "rc"}}

	if _, _, err := a.DownloadFile(context.Background(), msg); err == nil {
		t.Errorf("expected error on non-200 download URL, got nil")
	}
}

func TestDownloadFile_SSRFRejected(t *testing.T) {
	useTestHTTPClient(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "tok", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]string{"downloadUrl": "http://127.0.0.1:1/internal"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orig := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = orig }()

	a := &Adapter{clientID: "cid", clientSecret: "sec"}
	msg := &im.IncomingMessage{FileKey: "DL-CODE", FileName: "x.pdf", Extra: map[string]string{"robot_code": "rc"}}

	if _, _, err := a.DownloadFile(context.Background(), msg); err == nil {
		t.Fatal("expected SSRF rejection error, got nil")
	}
}

func TestIsAllowedDingTalkDownloadHost(t *testing.T) {
	cases := []struct {
		url   string
		allow bool
	}{
		{"https://wukong-abc.oss-cn-hangzhou.aliyuncs.com/file?sig=x", true},
		{"https://api.dingtalk.com/temp/file", true},
		{"http://127.0.0.1:8080/file", false},
	}
	for _, tc := range cases {
		if got := isAllowedDingTalkDownloadHost(tc.url); got != tc.allow {
			t.Errorf("isAllowedDingTalkDownloadHost(%q) = %v, want %v", tc.url, got, tc.allow)
		}
	}
}

// TestDownloadFile_URLRewrite covers the 专属钉 dedicated-line adaptation:
// the fake OpenAPI returns a dedicated-line downloadUrl which the adapter
// rewrites to the configured intranet base (here: the local test server).
// The real validateFileDownloadURL stays active, proving the rewritten
// target bypasses SSRF validation by design (operator-configured host).
func TestDownloadFile_URLRewrite(t *testing.T) {
	useRejectingFileFetchHTTPClient(t)
	fileBytes := []byte("rewritten intranet file bytes")

	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/temp/file" {
			_, _ = w.Write(fileBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer fileSrv.Close()

	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "tok", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"downloadUrl": "https://dingtalk-file.111.111.111.111:15443/temp/file?sig=abc",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	orig := apiBaseURL
	apiBaseURL = api.URL
	defer func() { apiBaseURL = orig }()

	cfg := &config.DingTalkConfig{
		DownloadURLRewrite: &config.DingTalkURLRewriteConfig{
			From: "https://dingtalk-file.111.111.111.111:15443,https://111.111.111.111:15443",
			To:   fileSrv.URL,
		},
	}
	a := NewWebhookAdapter("cid", "sec", "", cfg)
	msg := &im.IncomingMessage{FileKey: "DL-CODE", FileName: "x.pdf", Extra: map[string]string{"robot_code": "rc"}}

	reader, name, err := a.DownloadFile(context.Background(), msg)
	if err != nil {
		t.Fatalf("DownloadFile error: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(fileBytes) {
		t.Errorf("downloaded bytes = %q, want %q", got, fileBytes)
	}
	if name != "x.pdf" {
		t.Errorf("resolved name = %q, want %q", name, "x.pdf")
	}
}

// TestDownloadFile_TLSSkipVerify drives the file download against a TLS
// server with a self-signed certificate: with download_insecure_skip_verify
// enabled the download succeeds; with it disabled the TLS handshake fails.
func TestDownloadFile_TLSSkipVerify(t *testing.T) {
	useTestHTTPClient(t)
	fileBytes := []byte("tls intranet file bytes")

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fileBytes)
	}))
	defer tlsSrv.Close()

	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "tok", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"downloadUrl": "https://dingtalk-file.111.111.111.111:15443/temp/file",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	orig := apiBaseURL
	apiBaseURL = api.URL
	defer func() { apiBaseURL = orig }()

	base := config.DingTalkConfig{
		DownloadURLRewrite: &config.DingTalkURLRewriteConfig{
			From: "https://dingtalk-file.111.111.111.111:15443",
			To:   tlsSrv.URL,
		},
	}

	on := base
	on.DownloadInsecureSkipVerify = true
	a := NewWebhookAdapter("cid", "sec", "", &on)
	reader, _, err := a.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey: "DL-CODE", FileName: "x.pdf", Extra: map[string]string{"robot_code": "rc"},
	})
	if err != nil {
		t.Fatalf("DownloadFile with skip verify error: %v", err)
	}
	got, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}
	if string(got) != string(fileBytes) {
		t.Errorf("downloaded bytes = %q, want %q", got, fileBytes)
	}

	off := base
	a2 := NewWebhookAdapter("cid", "sec", "", &off)
	if _, _, err := a2.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey: "DL-CODE", FileName: "x.pdf", Extra: map[string]string{"robot_code": "rc"},
	}); err == nil {
		t.Error("expected TLS verification error with skip verify disabled, got nil")
	}
}
