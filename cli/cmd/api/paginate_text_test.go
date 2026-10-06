package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestAPI_PaginateFormats(t *testing.T) {
	one := []string{`{"data":[{"id":"1"}],"total":1,"page":1,"page_size":1}`}
	pages := []string{
		`{"data":[{"id":"1"}],"total":3,"page":1,"page_size":1}`,
		`{"data":[{"id":"2"}],"total":3,"page":2,"page_size":1}`,
		`{"data":[{"id":"3"}],"total":3,"page":3,"page_size":1}`,
	}
	empty := []string{`{"data":[],"total":0,"page":1,"page_size":1}`}
	merged := `{"data":[{"id":"1"},{"id":"2"},{"id":"3"}],"total":3}`
	envelope := `{"ok":true,"data":` + merged + `}`
	preciseData := `[9007199254740993,{"n":9007199254740993,"nested":[1.25e+3,true,null,"007"]}]`
	precise := []string{`{"data":` + preciseData + `,"total":2,"page_size":2}`}
	preciseMerged := `{"data":` + preciseData + `,"total":2}`

	cases := []struct {
		name     string
		bodies   []string
		flags    []string
		env      string
		json     string
		raw      string
		tty      bool
		post     bool
		single   bool
		wantExit int
	}{
		{
			name: "text single page", bodies: one, flags: []string{"--format", "text"},
			json: `{"data":[{"id":"1"}],"total":1}`,
		},
		{name: "text multiple capped pages", bodies: pages, flags: []string{"--format", "text"}, json: merged},
		{name: "text empty", bodies: empty, flags: []string{"--format", "text"}, json: `{"data":[],"total":0}`},
		{name: "text environment", bodies: pages, env: "text", json: merged},
		{
			name: "text flag overrides json environment", bodies: pages, env: "json",
			flags: []string{"--format", "text"}, json: merged,
		},
		{
			name: "json flag overrides text environment", bodies: pages, env: "text",
			flags: []string{"--format", "json"}, json: envelope,
		},
		{name: "default pipe", bodies: pages, json: envelope},
		{name: "default tty", bodies: pages, tty: true, json: envelope},
		{name: "json", bodies: pages, flags: []string{"--format", "json"}, json: envelope},
		{name: "ndjson", bodies: pages, flags: []string{"--format", "ndjson"}, json: merged},
		{
			name: "json jq", bodies: pages, env: "text",
			flags: []string{"--format", "json", "--jq", ".data.data | length"}, json: "3",
		},
		{
			name: "ndjson jq", bodies: pages, env: "text",
			flags: []string{"--format", "ndjson", "--jq", ".data | length"}, json: "3",
		},
		{
			name: "text environment jq rejected", bodies: pages, env: "text",
			flags: []string{"--jq", ".data | length"}, wantExit: 2,
		},
		{
			name: "text environment malformed jq rejected", bodies: pages, env: "text",
			flags: []string{"--jq", "["}, wantExit: 2,
		},
		{name: "text jq rejected", bodies: pages, flags: []string{"--format", "text", "--jq", ".data"}, wantExit: 2},
		{name: "text precise values", bodies: precise, flags: []string{"--format", "text"}, json: preciseMerged},
		{
			name: "json precise values", bodies: precise, flags: []string{"--format", "json"},
			json: `{"ok":true,"data":` + preciseMerged + `}`,
		},
		{name: "ndjson precise values", bodies: precise, flags: []string{"--format", "ndjson"}, json: preciseMerged},
		{
			name: "text no metadata", bodies: []string{` {"data":[9007199254740993]} `},
			flags: []string{"--format", "text"}, raw: " {\"data\":[9007199254740993]} \n",
		},
		{
			name: "text non json fallback", bodies: []string{"healthy"},
			flags: []string{"--format", "text"}, raw: "healthy\n",
		},
		{
			name: "text fallback existing newline", bodies: []string{"healthy\n"},
			flags: []string{"--format", "text"}, raw: "healthy\n",
		},
		{name: "text fallback empty body", bodies: []string{""}, flags: []string{"--format", "text"}},
		{name: "text non GET", bodies: one, flags: []string{"--format", "text"}, post: true, raw: one[0] + "\n"},
		{
			name: "text non paginated", bodies: one, flags: []string{"--format", "text"},
			single: true, raw: one[0] + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_FORMAT", tc.env)
			t.Setenv("WEKNORA_PROFILE", "")
			out, errOut := iostreams.SetForTest(t)
			if tc.tty {
				out, errOut = iostreams.SetForTestWithTTY(t)
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				assert.Equal(t, "/api/v1/items", r.URL.Path)
				assert.Equal(t, "active", r.URL.Query().Get("status"))
				method := http.MethodGet
				if tc.post {
					method = http.MethodPost
				}
				assert.Equal(t, method, r.Method)
				if !tc.post && !tc.single {
					assert.Equal(t, strconv.Itoa(n), r.URL.Query().Get("page"))
					assert.Equal(t, "50", r.URL.Query().Get("page_size"))
				} else {
					assert.Empty(t, r.URL.Query().Get("page"))
				}
				if n > len(tc.bodies) {
					t.Errorf("unexpected request %d", n)
					http.Error(w, "too many requests", http.StatusInternalServerError)
					return
				}
				_, _ = io.WriteString(w, tc.bodies[n-1])
			}))
			defer srv.Close()
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(srv.URL), nil }}
			root := cmd.NewRootCmd(f)
			root.SetOut(out)
			root.SetErr(errOut)
			args := append([]string{}, tc.flags...)
			args = append(args, "api", "/api/v1/items?status=active")
			if !tc.single {
				args = append(args, "--paginate")
			}
			if tc.post {
				args = append(args, "-X", "POST")
			}
			root.SetArgs(args)
			err := root.Execute()
			if tc.wantExit != 0 {
				require.Error(t, err)
				assert.Equal(t, tc.wantExit, cmdutil.ExitCode(err))
				assert.Contains(t, err.Error(), "--jq requires --format json|ndjson")
				assert.Zero(t, calls.Load())
				assert.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int32(len(tc.bodies)), calls.Load())
			assert.Empty(t, errOut.String())
			if tc.json == "" {
				assert.Equal(t, tc.raw, out.String())
				return
			}
			assert.True(t, strings.HasSuffix(out.String(), "\n"))
			// UseNumber keeps this comparison sensitive to RawMessage precision loss.
			decode := func(s string) any {
				d := json.NewDecoder(strings.NewReader(s))
				d.UseNumber()
				var v any
				require.NoError(t, d.Decode(&v))
				var extra any
				require.ErrorIs(t, d.Decode(&extra), io.EOF, "expected one JSON value")
				return v
			}
			assert.Equal(t, decode(tc.json), decode(out.String()))
			if !tc.tty {
				assert.Equal(t, 1, strings.Count(out.String(), "\n"))
			}
		})
	}
}

type paginationFailWriter struct {
	body bytes.Buffer
	err  error
	tail bool
}

func (w *paginationFailWriter) Write(p []byte) (int, error) {
	if w.tail {
		// Fail on the newline even if the encoder writes it with the body.
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			n, _ := w.body.Write(p[:i])
			return n, w.err
		}
		return w.body.Write(p)
	}
	return 0, w.err
}

func TestAPI_PaginateTextWriteError(t *testing.T) {
	for _, tail := range []bool{false, true} {
		t.Run(strconv.FormatBool(tail), func(t *testing.T) {
			t.Setenv("WEKNORA_PROFILE", "")
			iostreams.SetForTest(t)
			sentinel := errors.New("synthetic output failure")
			out := &paginationFailWriter{err: sentinel, tail: tail}
			iostreams.IO.Out = out
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"data":[{"id":"1"}],"total":1,"page_size":1}`)
			}))
			defer srv.Close()
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(srv.URL), nil }}
			root := cmd.NewRootCmd(f)
			root.SetArgs([]string{"api", "/api/v1/items", "--paginate", "--format", "text"})
			err := root.Execute()
			require.ErrorIs(t, err, sentinel)
			var typed *cmdutil.Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, cmdutil.CodeLocalFileIO, typed.Code)
			assert.Equal(t, 1, cmdutil.ExitCode(err))
			if tail {
				assert.NotEmpty(t, out.body.String())
			} else {
				assert.Empty(t, out.body.String())
			}
		})
	}
}
