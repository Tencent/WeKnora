package session

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseQARequestRejectsInvalidQuery(t *testing.T) {
	body, err := json.Marshal(CreateKnowledgeQARequest{Query: "invalid\x00query"})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/sessions/session-1/knowledge-qa", bytes.NewReader(body))
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}

	_, _, err = (&Handler{}).parseQARequest(ctx, "test")

	require.ErrorContains(t, err, "invalid characters")
}
