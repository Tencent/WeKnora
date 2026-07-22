package docs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	unifiedSearchRequestDefinition  = "github_com_Tencent_WeKnora_internal_types.UnifiedSearchRequest"
	unifiedSearchResponseDefinition = "github_com_Tencent_WeKnora_internal_types.UnifiedSearchResponse"
	unifiedSearchResultDefinition   = "github_com_Tencent_WeKnora_internal_types.UnifiedSearchResult"
)

func TestUnifiedSearchSwaggerContract(t *testing.T) {
	var document map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(SwaggerInfo.ReadDoc()), &document))

	definitions := swaggerMap(t, document["definitions"])
	request := swaggerMap(t, definitions[unifiedSearchRequestDefinition])
	require.Contains(t, request["required"], "query")

	properties := swaggerMap(t, request["properties"])
	require.Equal(t, float64(1), swaggerMap(t, properties["query"])["minLength"])
	require.Equal(t, float64(1), swaggerMap(t, properties["top_k"])["minimum"])
	require.Equal(t, float64(50), swaggerMap(t, properties["top_k"])["maximum"])
	require.Equal(t, float64(1), swaggerMap(t, properties["rrf_k"])["minimum"])
	require.Equal(t, float64(1000), swaggerMap(t, properties["rrf_k"])["maximum"])
	require.Equal(t, float64(0), swaggerMap(t, properties["rag_weight"])["minimum"])
	require.Equal(t, float64(0), swaggerMap(t, properties["wiki_weight"])["minimum"])

	paths := swaggerMap(t, document["paths"])
	path := swaggerMap(t, paths["/knowledge-bases/{id}/unified-search"])
	post := swaggerMap(t, path["post"])
	responses := swaggerMap(t, post["responses"])
	success := swaggerMap(t, responses["200"])
	schema := swaggerMap(t, success["schema"])
	require.Equal(t, "#/definitions/"+unifiedSearchResponseDefinition, schema["$ref"])

	response := swaggerMap(t, definitions[unifiedSearchResponseDefinition])
	responseProperties := swaggerMap(t, response["properties"])
	data := swaggerMap(t, responseProperties["data"])
	items := swaggerMap(t, data["items"])
	require.Equal(t, "#/definitions/"+unifiedSearchResultDefinition, items["$ref"])
	require.Contains(t, definitions, unifiedSearchResultDefinition)
}

func swaggerMap(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	result, ok := value.(map[string]interface{})
	require.True(t, ok, "expected Swagger object, got %T", value)
	return result
}
