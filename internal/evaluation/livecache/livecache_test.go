package livecache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlanIsDeterministicAndPaired(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	second, err := BuildPlan()
	require.NoError(t, err)
	require.Equal(t, p.SHA256, second.SHA256)
	require.Equal(t, "c2ce8945891a4513987e1e0d160c0d8eb262558e6eb902034923c725643bca78", p.SHA256)
	require.Len(t, p.Steps, 20)
	require.Less(t, p.OriginalPriceUpperBoundMicrounits, int64(1000000))
	require.Greater(t, p.SharedPrefixApproxTokens, 2048)
	for i := 4; i < len(p.Steps); i += 2 {
		a, b := p.Steps[i], p.Steps[i+1]
		require.Equal(t, a.Data, b.Data)
		require.Equal(t, a.InputTokenUpperBound, b.InputTokenUpperBound)
		require.Equal(t, a.Messages[0], b.Messages[0])
		require.NotEqual(t, a.Messages[1].Content, b.Messages[1].Content)
		require.Equal(t, len(a.Messages[1].Content), len(b.Messages[1].Content))
	}
}

func TestDryRunDoesNotReadCredentialsOrUseTransport(t *testing.T) {
	var count atomic.Int64
	dir := filepath.Join(t.TempDir(), "x08-dry")
	r, err := run(context.Background(), Options{OutputDir: dir}, func() (string, error) {
		t.Fatal("credential read during dry-run")
		return "", nil
	}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		count.Add(1)
		return nil, fmt.Errorf("network forbidden")
	})}, "offline-fixture")
	require.NoError(t, err)
	require.Equal(t, "dry-run", r.Mode)
	require.Zero(t, count.Load())
	require.Zero(t, r.ChatRequests+r.EmbeddingRequests)
	_, err = os.Stat(filepath.Join(dir, "x08.sqlite"))
	require.True(t, os.IsNotExist(err))
	_, err = Run(context.Background(), Options{OutputDir: dir}, nil)
	require.Error(t, err)
}

func TestExecutionRequiresExactPlanAndBudget(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	for _, o := range []Options{
		{Execute: true},
		{Execute: true, ConfirmPlanSHA256: p.SHA256},
		{Execute: true, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "0.000001"},
		{Execute: true, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "3.000001"},
	} {
		_, err := validateOptions(o, p)
		require.Error(t, err)
	}
	for _, v := range []string{"-1", "NaN", "1e3", "4", "1.0000001", ".5", "99999999999999999999999"} {
		_, err := ParseCNY(v)
		require.Error(t, err, v)
	}
	_, err = validateOptions(Options{Execute: true, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "1.00"}, p)
	require.NoError(t, err)
}

func TestWireRejectsOutputThinkingAndPayloadDrift(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	step := p.Steps[4]
	base := map[string]any{
		"model": ChatModel, "messages": step.Messages, "max_completion_tokens": 256,
		"enable_thinking": false, "temperature": 0.3,
	}
	b, _ := json.Marshal(base)
	require.NoError(t, validateWire("/compatible-mode/v1/chat/completions", b, step))
	for _, test := range []struct {
		key   string
		value any
	}{
		{"max_completion_tokens", 257},
		{"max_completion_tokens", 128},
		{"max_tokens", 256},
		{"max_tokens", 128},
		{"max_completion_tokens", nil},
		{"max_tokens", nil},
		{"top_p", 0.5},
		{"top_p", 0},
		{"enable_thinking", true},
		{"model", "different"},
		{"tools", []any{}},
		{"stream", true},
	} {
		modified := map[string]any{}
		for key, value := range base {
			modified[key] = value
		}
		modified[test.key] = test.value
		b, _ := json.Marshal(modified)
		require.Error(t, validateWire("/compatible-mode/v1/chat/completions", b, step), test.key)
	}
}

func TestWireRejectsEmbeddingDefaultDrift(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	step := p.Steps[0]
	base := map[string]any{
		"model": EmbeddingModel, "input": step.Input, "dimensions": 1024,
		"encoding_format": "float", "truncate_prompt_tokens": 511,
	}
	b, _ := json.Marshal(base)
	require.NoError(t, validateWire("/compatible-mode/v1/embeddings", b, step))
	for key, value := range map[string]any{
		"truncate_prompt_tokens": 512, "dimensions": 512, "encoding_format": "base64",
	} {
		modified := map[string]any{}
		for k, v := range base {
			modified[k] = v
		}
		modified[key] = value
		b, _ := json.Marshal(modified)
		require.Error(t, validateWire("/compatible-mode/v1/embeddings", b, step), key)
	}
	delete(base, "truncate_prompt_tokens")
	b, _ = json.Marshal(base)
	require.Error(t, validateWire("/compatible-mode/v1/embeddings", b, step))
}

func fixtureResponse(request *http.Request, mode string) (*http.Response, error) {
	var payload map[string]json.RawMessage
	body, _ := io.ReadAll(request.Body)
	_ = json.Unmarshal(body, &payload)
	var response any
	if strings.HasSuffix(request.URL.Path, "/embeddings") {
		var inputs []string
		_ = json.Unmarshal(payload["input"], &inputs)
		data := []any{}
		for i, text := range inputs {
			vector := make([]float32, 1024)
			for j := range vector {
				vector[j] = float32(len(text)+j+1) / 1024
			}
			data = append(data, map[string]any{"index": i, "embedding": vector})
		}
		response = map[string]any{
			"model": EmbeddingModel, "data": data,
			"usage": map[string]int{"prompt_tokens": 100, "total_tokens": 100},
		}
		if mode == "missing-embedding-usage" {
			delete(response.(map[string]any), "usage")
		}
	} else {
		var m []map[string]string
		_ = json.Unmarshal(payload["messages"], &m)
		content := m[len(m)-1]["content"]
		seat := regexp.MustCompile(`contains (\d+) seats`).FindStringSubmatch(content)[1]
		cache := 0
		if strings.HasPrefix(content, "<shared_source_contexts>") {
			cache = 2048
		}
		usage := map[string]any{
			"prompt_tokens": 5000, "completion_tokens": 50, "total_tokens": 5050,
			"prompt_tokens_details": map[string]int{"cached_tokens": cache},
		}
		if mode == "missing-cache-usage" {
			delete(usage, "prompt_tokens_details")
		}
		if mode == "all-misses" {
			usage["prompt_tokens_details"] = map[string]int{"cached_tokens": 0}
		}
		response = map[string]any{
			"model": ChatModel, "choices": []any{map[string]any{
				"finish_reason": "stop", "message": map[string]string{
					"role": "assistant", "content": fmt.Sprintf(
						"SUMMARY: A synthetic campus room provides seats and published opening hours "+
							"for its catalog entry.\nThe room contains %s seats and opens at 09:00 and "+
							"closes at 17:00.", seat),
				},
			}}, "usage": usage,
		}
	}
	b, _ := json.Marshal(response)
	status := http.StatusOK
	if mode == "http-error" {
		status = 503
		b = []byte(`{"error":{"message":"fixture error"}}`)
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(b)), Request: request,
	}, nil
}

func runFixture(t *testing.T, mode string) (*Result, string, int64, error) {
	t.Helper()
	logger.SetLogLevel(logger.LevelError)
	dir := filepath.Join(t.TempDir(), "x08-fixture")
	p, err := BuildPlan()
	require.NoError(t, err)
	var count atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		require.Equal(t, "dashscope.aliyuncs.com", r.URL.Host)
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "Bearer x08-fixture-credential", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-X08-Gate"))
		db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "x08.sqlite")),
			&gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
		require.NoError(t, err)
		var reserved int64
		require.NoError(t, db.Model(&Reservation{}).Where("state = ?", "reserved").Count(&reserved).Error)
		require.Equal(t, int64(1), reserved, "reservation must exist before outbound request")
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		return fixtureResponse(r, mode)
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := run(context.Background(), Options{
		Execute: true, OutputDir: dir, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "1.00",
	}, func() (string, error) { return "x08-fixture-credential", nil }, client, "offline-fixture")
	return r, dir, count.Load(), err
}

func TestProductionPipelineOfflineFixture(t *testing.T) {
	r, dir, count, err := runFixture(t, "")
	require.NoError(t, err)
	require.Equal(t, "offline-fixture", r.Mode)
	require.Equal(t, int64(18), count)
	require.Equal(t, 2, r.EmbeddingRequests)
	require.Equal(t, 16, r.ChatRequests)
	require.Equal(t, "physical_calls_reduced", r.EmbeddingOutcome)
	require.Equal(t, "observed_stable_prefix_advantage", r.WikiOutcome)
	require.Equal(t, int64(18), r.LedgerCost.AccountingCompleteCalls)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		require.NoError(t, err)
		require.NotContains(t, string(b), "x08-fixture-credential", f.Name())
	}
}

func TestSeparateCredentialsStayAtProviderBoundary(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "separate-keys")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		expected := "fixture-chat-only"
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			expected = "fixture-embedding-only"
		}
		require.Equal(t, "Bearer "+expected, r.Header.Get("Authorization"))
		return fixtureResponse(r, "")
	})}
	result, err := run(context.Background(), Options{
		Execute: true, OutputDir: dir, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "0.081",
		EmbeddingCredential: func() (string, error) { return "fixture-embedding-only", nil },
	}, func() (string, error) { return "fixture-chat-only", nil }, client, "offline-fixture")
	require.NoError(t, err)
	require.Equal(t, 2, result.EmbeddingRequests)
	require.Equal(t, 16, result.ChatRequests)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		require.NoError(t, err)
		require.NotContains(t, string(data), "fixture-chat-only")
		require.NotContains(t, string(data), "fixture-embedding-only")
	}
}

func TestSeparateEmbeddingCredentialRespectsPreflightAndValidation(t *testing.T) {
	p, err := BuildPlan()
	require.NoError(t, err)
	for _, execute := range []bool{false, true} {
		_, err := Run(context.Background(), Options{
			Execute: execute, OutputDir: filepath.Join(t.TempDir(), "preflight"),
			EmbeddingCredential: func() (string, error) {
				t.Fatal("embedding credential read before approval")
				return "", nil
			},
		}, nil)
		if execute {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	for _, invalid := range []string{"", "bad\nheader"} {
		_, err := Run(context.Background(), Options{
			Execute: true, ConfirmPlanSHA256: p.SHA256, ApprovedCNY: "0.081",
			OutputDir:           filepath.Join(t.TempDir(), "invalid"),
			EmbeddingCredential: func() (string, error) { return invalid, nil },
		}, func() (string, error) { return "fixture-chat-only", nil })
		require.ErrorContains(t, err, "embedding credential")
	}
}

func TestUnknownUsageAndFailureStopSubsequentRequests(t *testing.T) {
	for _, test := range []struct {
		mode   string
		count  int64
		reason string
	}{
		{"missing-embedding-usage", 1, "usage_unreported"},
		{"missing-cache-usage", 3, "cache_usage_unreported"},
		{"http-error", 1, "provider_http_failed"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			r, _, count, err := runFixture(t, test.mode)
			require.Error(t, err)
			require.Equal(t, test.count, count)
			require.Equal(t, test.reason, r.StopReason)
			require.Greater(t, r.ReservedMicrounits, int64(0))
			if test.mode == "missing-cache-usage" {
				last := r.Steps[len(r.Steps)-1]
				require.NotNil(t, last.PromptTokens)
				require.Nil(t, last.CacheReadTokens)
				require.Nil(t, last.CostMicrounits)
			}
		})
	}
}

func TestNoCacheHitsRemainInconclusive(t *testing.T) {
	r, _, count, err := runFixture(t, "all-misses")
	require.Error(t, err)
	require.Equal(t, int64(18), count)
	require.Equal(t, "no_cache_hits_observed", r.WikiOutcome)
	require.Equal(t, "inconclusive", r.Status)
}

func TestGatewayRejectsRepeatedAttemptWithoutExternalCall(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "x08-gate.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Reservation{}))
	sqlDB, _ := db.DB()
	defer func() { require.NoError(t, sqlDB.Close()) }()
	p, err := BuildPlan()
	require.NoError(t, err)
	step := p.Steps[0]
	ctx := types.WithModelAccountingState(context.Background())
	var count atomic.Int64
	g := &gateway{
		db: db, budget: p.OriginalPriceUpperBoundMicrounits, nonce: "fixture",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			count.Add(1)
			return fixtureResponse(r, "")
		})},
	}
	require.NoError(t, g.arm(ctx, &step))
	b, _ := json.Marshal(map[string]any{
		"model": EmbeddingModel, "input": step.Input, "dimensions": 1024,
		"encoding_format": "float", "truncate_prompt_tokens": 511,
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/compatible-mode/v1/embeddings", bytes.NewReader(b))
		req.Header.Set("X-X08-Gate", "fixture")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if i == 0 {
			require.Equal(t, 200, w.Code)
		} else {
			require.Equal(t, 400, w.Code)
		}
	}
	require.Equal(t, int64(1), count.Load())
	require.ErrorIs(t, types.ModelAccountingError(ctx), types.ErrModelAccounting)
}

func TestReservationFailureAndBudgetExhaustionMakeNoRequest(t *testing.T) {
	for _, kind := range []string{"budget", "storage"} {
		t.Run(kind, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "x08-reserve.sqlite")),
				&gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
			require.NoError(t, err)
			sqlDB, _ := db.DB()
			defer func() { require.NoError(t, sqlDB.Close()) }()
			if kind == "budget" {
				require.NoError(t, db.AutoMigrate(&Reservation{}))
			}
			p, err := BuildPlan()
			require.NoError(t, err)
			step := p.Steps[0]
			var count atomic.Int64
			budget := p.OriginalPriceUpperBoundMicrounits
			if kind == "budget" {
				budget = step.ReservationMicrounits - 1
			}
			g := &gateway{
				db: db, budget: budget, nonce: "fixture",
				client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					count.Add(1)
					return fixtureResponse(r, "")
				})},
			}
			require.NoError(t, g.arm(types.WithModelAccountingState(context.Background()), &step))
			b, _ := json.Marshal(map[string]any{
				"model": EmbeddingModel, "input": step.Input, "dimensions": 1024,
				"encoding_format": "float", "truncate_prompt_tokens": 511,
			})
			req := httptest.NewRequest("POST", "/compatible-mode/v1/embeddings", bytes.NewReader(b))
			req.Header.Set("X-X08-Gate", "fixture")
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.Zero(t, count.Load())
			_, _, reserved, _ := g.state()
			require.Zero(t, reserved)
		})
	}
}

func TestProviderClientDoesNotFollowRedirects(t *testing.T) {
	client := newProviderClient()
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy)
	var count atomic.Int64
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		return &http.Response{
			StatusCode: 302, Header: http.Header{"Location": []string{"https://example.invalid/unapproved"}},
			Body: io.NopCloser(strings.NewReader("")), Request: r,
		}, nil
	})
	req, err := http.NewRequest("POST", Endpoint+"/embeddings", strings.NewReader("{}"))
	require.NoError(t, err)
	response, err := client.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, 302, response.StatusCode)
	require.Equal(t, int64(1), count.Load())
}
