package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestSQLNestedQueryBoundaries(t *testing.T) {
	for _, query := range []string{
		"SELECT * FROM (WITH x AS (SELECT * FROM read_text('/tmp/forbidden')) SELECT * FROM x) q, allowed",
		"SELECT * FROM (SELECT * FROM allowed UNION SELECT * FROM read_text('/tmp/forbidden')) q, allowed",
		"SELECT * FROM (SELECT * FROM victim_table) q, allowed",
		"SELECT (SELECT secret FROM victim_table) FROM allowed",
		"SELECT * FROM allowed LIMIT (SELECT secret FROM victim_table)",
		`SELECT * FROM "ALLOWED"`,
	} {
		_, validation := ValidateSQL(query, WithSelectOnly(), WithSingleStatement(),
			WithAllowedTables("allowed"), WithNoDangerousFunctions())
		require.False(t, validation.Valid, query)
	}
	_, validation := ValidateSQL("SELECT * FROM (SELECT id FROM allowed) q, allowed",
		WithSelectOnly(), WithAllowedTables("allowed"), WithNoDangerousFunctions())
	require.True(t, validation.Valid, "%+v", validation.Errors)
}

func TestSQLPredicatesCannotBecomeQuotedText(t *testing.T) {
	for _, query := range []string{
		`SELECT id AS "ORDER BY fake" FROM knowledges`,
		`SELECT 'WHERE ORDER BY' AS name FROM knowledges WHERE title = 'WHERE LIMIT' OR title = 'other'`,
		`SELECT "ORDER BY".id FROM knowledges AS "ORDER BY"`,
		`SELECT a.id FROM knowledges a JOIN knowledges b ON a.id = b.id`,
	} {
		secured, validation, err := ValidateAndSecureSQL(query, WithSecurityDefaults(7),
			WithSoftDeleteFilter(), WithSearchScopeFilter([]string{"kb-1"}, nil))
		require.NoError(t, err, "%+v", validation.Errors)
		parsed, err := pg_query.Parse(secured)
		require.NoError(t, err)
		require.NotNil(t, parsed.Stmts[0].Stmt.GetSelectStmt().WhereClause, secured)
		require.Contains(t, secured, "tenant_id = 7")
		require.Contains(t, secured, "deleted_at IS NULL")
		require.Contains(t, secured, "knowledge_base_id IN ('kb-1')")
		if strings.Contains(query, "JOIN") {
			require.Contains(t, secured, "a.tenant_id = 7")
			require.Contains(t, secured, "b.tenant_id = 7")
		}
	}
	require.Empty(t, InjectAndConditions("invalid", "tenant_id = 1"))
}

func TestSSRFCrossOriginRedirect(t *testing.T) {
	reached := false
	sink := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached = true }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	// Test the redirect policy with a plain local transport; target blocking
	// must happen even when both destinations would otherwise be trusted.
	client := &http.Client{CheckRedirect: newSSRFCheckRedirect(10)}
	_, err := client.Post(source.URL, "application/json", strings.NewReader(`{"client_secret":"synthetic"}`))
	require.Error(t, err)
	require.False(t, reached)
	req, err := http.NewRequest("GET", "https://example.com", nil)
	require.NoError(t, err)
	req.Header.Set("Custom-Secret-Header", "synthetic")
	req.Header.Set("Authorization", "Bearer synthetic")
	req.Header.Set("Accept", "application/json")
	stripRedirectSensitiveHeaders(req)
	require.Empty(t, req.Header.Get("Custom-Secret-Header"))
	require.Empty(t, req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Accept"))
}

func TestKnownExampleKeyCannotSign(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "weknora-system-aes-key-32bytes!!")
	require.Nil(t, SystemHMACKey())
	_, err := SignFileURL("https://example.com", "local://1/a", 1, 0)
	require.Error(t, err)
}
