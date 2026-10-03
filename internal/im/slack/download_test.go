package slack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	slacklib "github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
)

type fileDownloadTransport func(*http.Request) (*http.Response, error)

func (f fileDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadFileTrustedEndpoints(t *testing.T) {
	for _, target := range []string{
		"https://files.slack.com/files-pri/T-F/download/test.txt",
		"https://FILES.SLACK.COM:443/files-pri/T-F/download/test.txt",
		"https://slack.com/files-pri/T-F/download/test.txt",
	} {
		for _, fromInfo := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fromInfo=%t", target, fromInfo), func(t *testing.T) {
				transport := fileDownloadTransport(func(req *http.Request) (*http.Response, error) {
					body := "file contents"
					if strings.HasSuffix(req.URL.Path, "/files.info") {
						body = fmt.Sprintf(`{"ok":true,"file":{"url_private_download":%q}}`, target)
					} else {
						if req.URL.String() != target || req.Header.Get("Authorization") != "Bearer test-token" {
							return nil, fmt.Errorf("unexpected download URL or authorization")
						}
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				client := &http.Client{Transport: transport}
				adapter := &Adapter{api: slacklib.New("test-token", slacklib.OptionHTTPClient(client))}
				msg := &im.IncomingMessage{FileKey: "F", FileName: "test.txt"}
				if !fromInfo {
					msg.Extra = map[string]string{"url_private_download": target}
				}
				reader, name, err := adapter.DownloadFile(context.Background(), msg)
				require.NoError(t, err)
				defer func() { require.NoError(t, reader.Close()) }()
				body, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.Equal(t, "file contents", string(body))
				require.Equal(t, "test.txt", name)
			})
		}
	}
}

func TestDownloadFileRejectsNonDownloadOrigins(t *testing.T) {
	for _, target := range []string{
		"https://files.slack.com.evil.example/file", "https://files.slack.com:444/file",
		"https://user@files.slack.com/file", "http://files.slack.com/file",
		"https://slack.com/redirect", "https://workspace.slack.com/files/U/F",
		"https://slack-files.com/T-F-public", "https://slack.com.evil.example/files-pri/file",
	} {
		// A nil SDK client proves rejection precedes any authenticated download.
		_, _, err := (&Adapter{}).DownloadFile(context.Background(), &im.IncomingMessage{
			FileKey: "F", Extra: map[string]string{"url_private_download": target},
		})
		require.Error(t, err, target)
	}
}
