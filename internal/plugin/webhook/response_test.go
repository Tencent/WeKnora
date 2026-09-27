package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A plugin's HTML answer reaches the caller as sent, but inert in a browser.
func TestWriteResponseSandboxesTheAnswer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		status    int
		ctype     string
		wantCode  int
		wantCtype string
	}{
		{http.StatusAccepted, "text/html; charset=utf-8", http.StatusAccepted, "text/html; charset=utf-8"},
		{0, "", http.StatusOK, "application/octet-stream"},
		{999, "application/json", http.StatusOK, "application/json"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		WriteResponse(c, tc.status, tc.ctype, []byte("<script>alert(1)</script>"))
		if w.Code != tc.wantCode || w.Header().Get("Content-Type") != tc.wantCtype {
			t.Fatalf("%+v: code %d type %q", tc, w.Code, w.Header().Get("Content-Type"))
		}
		if w.Body.String() != "<script>alert(1)</script>" {
			t.Fatalf("body = %q", w.Body.String())
		}
		for k, v := range map[string]string{
			"Content-Security-Policy": callbackCSP, "X-Content-Type-Options": "nosniff",
			"X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
		} {
			if got := w.Header().Get(k); got != v {
				t.Fatalf("%s = %q, want %q", k, got, v)
			}
		}
	}
}
