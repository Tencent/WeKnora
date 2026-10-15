package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiagnosticProjectionOmitsInfrastructureSecrets(t *testing.T) {
	for _, tc := range []struct{ operation, body, visible string }{
		{"system.info", `{"data":{"version":"v1","edition":"lite","db_migration_error":"SECRET"}}`, "v1"},
		{"system.parsers", `{"data":[{"Name":"pdf","Available":true,"UnavailableReason":"SECRET"}],` +
			`"connected":true,"docreader_addr":"SECRET"}`, "pdf"},
		{"sandbox.list", `{"data":[{"id":"s1","name":"Sandbox","config":{"api_key":"SECRET"}}]}`, "Sandbox"},
		{"storage.list", `{"data":[{"id":"b1","name":"Storage","config":{"secret":"SECRET"}}]}`, "Storage"},
		{"vector_store.list", `{"data":[{"id":"v1","name":"Vector","connection_config":{"url":"SECRET"}}]}`, "Vector"},
		{"data_source.get", `{"id":"d1","name":"Source","config":{"settings":"SECRET"},` +
			`"credentials":{"token":"SECRET"},"error_message":"SECRET","last_sync_cursor":"SECRET"}`, "Source"},
		{"data_source.log", `{"id":"l1","status":"failed","error_message":"SECRET",` +
			`"result":{"token":"SECRET"}}`, "failed"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			op, ok := Builtin().Get(tc.operation)
			require.True(t, ok)
			result, err := op.Project([]byte(tc.body))
			require.NoError(t, err)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.Contains(t, string(encoded), tc.visible)
			require.NotContains(t, string(encoded), "SECRET")
		})
	}
}

func TestInfrastructureDiscoveryDoesNotOpenConfigurationWrites(t *testing.T) {
	for _, id := range []string{
		"system.info", "sandbox.list", "storage.list", "vector_store.list", "data_source.list",
	} {
		op, ok := Builtin().Get(id)
		require.True(t, ok)
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			changed := op
			changed.ID = "attempt.modify"
			changed.Method = method
			changed.Risk = RiskDestructive
			changed.Target = &Target{Operation: id, Label: "name"}
			_, err := NewCatalog(op, changed)
			require.Error(t, err)
		}
	}
	for _, route := range []string{
		"/api/v1/system/admin/users", "/api/v1/system/parser-engines/check",
		"/api/v1/sandbox-configs/workspace-policy", "/api/v1/datasource/:id/credentials",
		"/api/v1/vector-stores/:id", "/api/v1/storage-backends/:id/test",
	} {
		_, err := parseRoute(route)
		require.Error(t, err, route)
	}
}
