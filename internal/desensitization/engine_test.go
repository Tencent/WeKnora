package desensitization

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyDisabledIsNoop(t *testing.T) {
	out, report, err := Apply(context.Background(), "13800138000", types.DesensitizationConfig{}, Deps{})
	require.NoError(t, err)
	assert.Equal(t, "13800138000", out)
	assert.Equal(t, false, report["masked"])
}

func TestApplyBuiltinEngine(t *testing.T) {
	out, _, err := Apply(context.Background(), "请拨打13800138000", types.DesensitizationConfig{
		Enabled:     true,
		EntityTypes: []string{types.DesensitizationEntityCNMobile},
	}, Deps{})
	require.NoError(t, err)
	assert.Contains(t, out, "<手机号>")
	assert.NotContains(t, out, "13800138000")
}

func TestSelectBuiltinByDefault(t *testing.T) {
	eng, err := Select(types.DesensitizationConfig{Enabled: true}, Deps{})
	require.NoError(t, err)
	assert.Equal(t, types.DesensitizationEngineBuiltin, eng.Name())
}

func TestValidateConfigRejectsCloudParserAndBadRegexp(t *testing.T) {
	cfg := &types.DesensitizationConfig{Enabled: true}
	err := ValidateConfig(cfg, types.ChunkingConfig{
		ParserEngineRules: []types.ParserEngineRule{{Engine: "mineru_cloud", FileTypes: []string{"pdf"}}},
	})
	require.ErrorIs(t, err, ErrCloudParserForbidden)

	cfg = &types.DesensitizationConfig{
		Enabled: true,
		Rules:   []types.DesensitizationRule{{Name: "bad", Pattern: "("}},
	}
	err = ValidateConfig(cfg, types.ChunkingConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")

	require.NoError(t, ValidateAgainstParsers(cfg, types.ChunkingConfig{
		ParserEngineRules: []types.ParserEngineRule{{Engine: "anydoc", FileTypes: []string{"pdf"}}},
	}))
	require.NoError(t, ValidateParserEngine(cfg, "anydoc"))
	require.ErrorIs(t, ValidateParserEngine(&types.DesensitizationConfig{Enabled: true}, "weknoracloud"), ErrCloudParserForbidden)
}

func TestApplyPresidioMergesAnalyzerHits(t *testing.T) {
	text := "邮箱 user@example.com"
	prefix := []rune("邮箱 ")
	email := []rune("user@example.com")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/analyze", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]any{{
			"start": len(prefix), "end": len(prefix) + len(email), "score": 0.99, "entity_type": "EMAIL_ADDRESS",
		}}))
	}))
	t.Cleanup(srv.Close)

	out, report, err := Apply(context.Background(), text, types.DesensitizationConfig{
		Enabled:     true,
		Engine:      types.DesensitizationEnginePresidio,
		EntityTypes: []string{types.DesensitizationEntityEmail},
	}, Deps{PresidioAnalyzerURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)
	assert.Contains(t, out, "<邮箱>")
	assert.NotContains(t, out, "user@example.com")
	assert.Equal(t, types.DesensitizationEnginePresidio, report["engine"])
}

func TestApplyPresidioUsesRuneOffsetsWithCJKPrefix(t *testing.T) {
	text := "中文 +1 212-555-5555"
	phone := "+1 212-555-5555"
	runes := []rune(text)
	start := -1
	for i := 0; i <= len(runes)-len([]rune(phone)); i++ {
		if string(runes[i:i+len([]rune(phone))]) == phone {
			start = i
			break
		}
	}
	require.GreaterOrEqual(t, start, 0)
	end := start + len([]rune(phone))
	require.NotEqual(t, start, len("中文 "), "byte offset must differ from rune offset")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]any{{
			"start": start, "end": end, "score": 0.99, "entity_type": "PHONE_NUMBER",
		}}))
	}))
	t.Cleanup(srv.Close)

	out, _, err := Apply(context.Background(), text, types.DesensitizationConfig{
		Enabled:     true,
		Engine:      types.DesensitizationEnginePresidio,
		EntityTypes: []string{types.DesensitizationEntityCNMobile},
	}, Deps{PresidioAnalyzerURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)
	assert.Contains(t, out, "中文")
	assert.Contains(t, out, "<手机号>")
	assert.NotContains(t, out, "212-555-5555")
	assert.NotContains(t, out, "+1 212")
}

func TestApplyPresidioFailsClosedWithoutURL(t *testing.T) {
	_, _, err := Apply(context.Background(), "a", types.DesensitizationConfig{
		Enabled: true,
		Engine:  types.DesensitizationEnginePresidio,
	}, Deps{})
	require.ErrorIs(t, err, ErrEngineUnavailable)
}

func TestApplyLLMUsesMatches(t *testing.T) {
	complete := Completer(func(ctx context.Context, _, _ string) (string, error) {
		return `{"matches":[{"text":"张三密文999","type":"email"}]}`, nil
	})
	out, _, err := Apply(context.Background(), "联系张三密文999即可", types.DesensitizationConfig{
		Enabled:     true,
		Engine:      types.DesensitizationEngineLLM,
		EntityTypes: []string{types.DesensitizationEntityEmail},
	}, Deps{Complete: complete})
	require.NoError(t, err)
	assert.Contains(t, out, "<邮箱>")
	assert.NotContains(t, out, "张三密文999")
}

func TestApplyLLMFailsClosedOnBadJSON(t *testing.T) {
	complete := Completer(func(ctx context.Context, _, _ string) (string, error) {
		return "not-json", nil
	})
	_, _, err := Apply(context.Background(), "abc", types.DesensitizationConfig{
		Enabled: true,
		Engine:  types.DesensitizationEngineLLM,
	}, Deps{Complete: complete})
	require.ErrorIs(t, err, ErrEngineUnavailable)
}
