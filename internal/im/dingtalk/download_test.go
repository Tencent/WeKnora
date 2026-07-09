package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// TestDownloadFile_EndToEnd drives the full DownloadFile orchestration against a
// fake DingTalk OpenAPI: access-token fetch → messageFiles/download (downloadCode
// → temporary downloadUrl) → GET the temp URL for the bytes. This covers the HTTP
// path that the pure-function unit tests deliberately skip (issue #1771).
func TestDownloadFile_EndToEnd(t *testing.T) {
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
