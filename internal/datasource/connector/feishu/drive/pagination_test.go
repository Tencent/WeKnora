package drive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
)

// TestClientDriveListFilesPagination is the Drive (云盘) counterpart of the wiki
// test added in #3645 (TestClientWikiPaginationRejectsRepeatedPageToken):
// ListDriveFilesAllPages used to stop only on an empty next_page_token, so a
// repeated token (or has_more=false with a stale token) made it re-request the
// same page forever. Each case runs under a short deadline so a regression
// fails fast instead of hanging the suite.
func TestClientDriveListFilesPagination(t *testing.T) {
	tests := []struct {
		name         string
		response     func(page int) core.DriveFileListResponse
		wantErr      string
		wantRequests int
		wantFiles    []string
	}{
		{
			// Feishu can hand back has_more=true with a token it already
			// returned; that is exactly what #3645 hit on the wiki endpoints.
			name: "repeated page token",
			response: func(page int) core.DriveFileListResponse {
				return core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files:         []core.DriveFile{{Token: fmt.Sprintf("file-%d", page)}},
						HasMore:       true,
						NextPageToken: "repeated-token",
					},
				}
			},
			wantErr:      "repeated page token",
			wantRequests: 2,
		},
		{
			// has_more=false is the authoritative stop signal: a residual
			// non-empty token must not trigger another round trip.
			name: "has_more false with stale token",
			response: func(int) core.DriveFileListResponse {
				return core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files:         []core.DriveFile{{Token: "file-1"}},
						HasMore:       false,
						NextPageToken: "stale-token",
					},
				}
			},
			wantRequests: 1,
			wantFiles:    []string{"file-1"},
		},
		{
			// The guard must not truncate a genuinely paginated folder.
			name: "two real pages are still merged",
			response: func(page int) core.DriveFileListResponse {
				if page == 1 {
					return core.DriveFileListResponse{
						ApiResponse: core.ApiResponse{Code: 0},
						Data: core.DriveFileListData{
							Files:         []core.DriveFile{{Token: "file-1"}},
							HasMore:       true,
							NextPageToken: "token-2",
						},
					}
				}
				return core.DriveFileListResponse{
					ApiResponse: core.ApiResponse{Code: 0},
					Data: core.DriveFileListData{
						Files:         []core.DriveFile{{Token: "file-2"}},
						HasMore:       false,
						NextPageToken: "",
					},
				}
			},
			wantRequests: 2,
			wantFiles:    []string{"file-1", "file-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			authPath := "/open-apis/auth/v3/tenant_access_token/internal"
			mux.HandleFunc(authPath, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, core.TokenResponse{
					ApiResponse:       core.ApiResponse{Code: 0},
					TenantAccessToken: "fake-token",
					Expire:            7200,
				})
			})
			var requests atomic.Int64
			mux.HandleFunc("/open-apis/drive/v1/files", func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, tt.response(int(requests.Add(1))))
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			client := core.NewClient(&core.Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL})

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			files, err := client.ListDriveFilesAllPages(ctx, "folder1")
			if ctx.Err() != nil {
				t.Fatalf("pagination never terminated (requests=%d, err=%v)", requests.Load(), err)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ListDriveFilesAllPages() error = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ListDriveFilesAllPages() error = %v, want error containing %q", err, tt.wantErr)
			}
			if got := int(requests.Load()); got != tt.wantRequests {
				t.Fatalf("drive list requests = %d, want %d", got, tt.wantRequests)
			}

			gotFiles := make([]string, 0, len(files))
			seen := make(map[string]bool, len(files))
			for _, f := range files {
				if seen[f.Token] {
					t.Fatalf("file token %q was appended more than once", f.Token)
				}
				seen[f.Token] = true
				gotFiles = append(gotFiles, f.Token)
			}
			if tt.wantFiles != nil && !reflect.DeepEqual(gotFiles, tt.wantFiles) {
				t.Fatalf("files = %v, want %v", gotFiles, tt.wantFiles)
			}
		})
	}
}
