package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
)

func TestTenantAccessTokenNeverAppearsInLogs(t *testing.T) {
	for _, token := range []string{"synthetic-access-value-for-logging-test", "xyz"} {
		t.Run(token, func(t *testing.T) {
			var logs bytes.Buffer
			recorder := logrus.New()
			recorder.SetOutput(&logs)
			recorder.SetLevel(logrus.DebugLevel)
			ctx := context.WithValue(context.Background(), types.LoggerContextKey, logrus.NewEntry(recorder))
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/open-apis/auth/v3/tenant_access_token/internal" || r.Method != http.MethodPost {
					t.Errorf("unexpected token request")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": token, "expire": 7200})
			}))
			defer server.Close()
			client := &Client{
				baseURL:    server.URL,
				appID:      "test-app",
				appSecret:  "synthetic-credential",
				httpClient: server.Client(),
			}
			for i := 0; i < 2; i++ {
				got, err := client.GetTenantAccessToken(ctx)
				if err != nil || got != token {
					t.Fatalf("token retrieval or cache failed: %v", err)
				}
			}
			if calls != 1 {
				t.Errorf("cached token should avoid a second request; got %d", calls)
			}
			prefix, suffix := token[:min(8, len(token))], token[max(0, len(token)-4):]
			for _, secret := range []string{token, prefix, suffix, "synthetic-credential"} {
				if strings.Contains(logs.String(), secret) {
					t.Error("authentication secret or fragment was logged")
				}
			}
			if !strings.Contains(logs.String(), "expire=7200s") {
				t.Error("safe token expiry diagnostics must remain")
			}
		})
	}
}
