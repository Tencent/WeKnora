package confluence

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestPATAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		credentials map[string]interface{}
		want        string
		invalid     bool
	}{
		{
			name: "PAT without username",
			credentials: map[string]interface{}{
				"token": " pat-value ",
			},
			want: "Bearer pat-value",
		},
		{
			name: "PAT overrides password",
			credentials: map[string]interface{}{
				"username": "reader",
				"password": "password",
				"token":    "pat-value",
			},
			want: "Bearer pat-value",
		},
		{
			name: "legacy Basic",
			credentials: map[string]interface{}{
				"username": "reader",
				"password": "password",
			},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:password")),
		},
		{
			name: "blank PAT falls back to Basic",
			credentials: map[string]interface{}{
				"username": "reader",
				"password": "password",
				"token":    "  ",
			},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:password")),
		},
		{
			name: "Cloud ignores Server PAT",
			credentials: map[string]interface{}{
				"edition":   "cloud",
				"username":  "reader",
				"api_token": "cloud-token",
				"token":     "pat-value",
			},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:cloud-token")),
		},
		{
			name: "Cloud still requires username",
			credentials: map[string]interface{}{
				"edition":   "cloud",
				"api_token": "cloud-token",
				"token":     "pat-value",
			},
			invalid: true,
		},
		{
			name: "Cloud still requires API token",
			credentials: map[string]interface{}{
				"edition":  "cloud",
				"username": "reader",
				"token":    "pat-value",
			},
			invalid: true,
		},
		{
			name: "Basic still requires username",
			credentials: map[string]interface{}{
				"password": "password",
			},
			invalid: true,
		},
		{
			name: "Basic still requires password",
			credentials: map[string]interface{}{
				"username": "reader",
			},
			invalid: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.credentials["base_url"] = testBaseURL
			cfg, err := parseConfig(&types.DataSourceConfig{Credentials: tc.credentials})
			if tc.invalid {
				if !errors.Is(err, datasource.ErrInvalidCredentials) {
					t.Fatalf("expected invalid credentials, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var calls int
			transport := roundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if got := req.Header.Get("Authorization"); got != tc.want {
					t.Errorf("authorization = %q, want %q", got, tc.want)
				}
				if strings.Contains(req.URL.Path, "download") {
					return imageResp(http.StatusOK, "image/png", testPNG), nil
				}
				return imageResp(http.StatusOK, "application/json", []byte("{}")), nil
			})
			c := &client{cfg: cfg, http: &http.Client{Transport: transport}}
			if err := c.ping(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, _, err := c.downloadImage(context.Background(), testBaseURL+"/download/image.png"); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("got %d requests, want API and image requests", calls)
			}
		})
	}
}

func TestPATPickerCachePartitionsAuthentication(t *testing.T) {
	keys := make(map[string]bool)
	for _, creds := range []map[string]interface{}{
		{"username": "reader", "password": "same-secret"},
		{"username": "reader", "token": "same-secret"},
		{"username": "reader", "token": "rotated-secret"},
	} {
		creds["base_url"] = testBaseURL
		cfg, err := parseConfig(&types.DataSourceConfig{Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		key := pickerSpaceCacheKey(cfg)
		if keys[key] {
			t.Fatal("different authentication contexts share a cache key")
		}
		if strings.Contains(key, "same-secret") || strings.Contains(key, "rotated-secret") {
			t.Fatal("cache key contains credential text")
		}
		keys[key] = true
	}
}

func TestPATImageRedirectBoundaries(t *testing.T) {
	for _, target := range []string{
		testBaseURL + "/download/final.png",
		"https://other.test/image.png",
		"https://confluence.test/other-app/image.png",
	} {
		t.Run(target, func(t *testing.T) {
			cfg, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
				"base_url": testBaseURL, "token": "pat-value",
			}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			transport := roundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasPrefix(req.URL.String(), testBaseURL+"/") {
					t.Fatal("request escaped configured Confluence scope")
				}
				if req.Header.Get("Authorization") != "Bearer pat-value" {
					t.Error("missing PAT")
				}
				if calls == 1 {
					return redirectResp(target), nil
				}
				return imageResp(http.StatusOK, "image/png", testPNG), nil
			})
			c := &client{cfg: cfg, http: &http.Client{Transport: transport}}
			_, _, err = c.downloadImage(context.Background(), testBaseURL+"/download/start.png")
			if strings.HasPrefix(target, testBaseURL+"/") {
				if err != nil || calls != 2 {
					t.Fatalf("same-scope redirect: calls=%d err=%v", calls, err)
				}
			} else if !errors.Is(err, errImageRedirectOffOrigin) || calls != 1 {
				t.Fatalf("unsafe redirect: calls=%d err=%v", calls, err)
			}
		})
	}
}
