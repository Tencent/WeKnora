package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMCPFullSchemaValidation(t *testing.T) {
	cases := []struct{ name, schema, valid, invalid string }{
		{"nested required", `{
  "type": "object",
  "properties": {
    "customer": {
      "type": "object",
      "properties": {
        "id": {
          "type": "string"
        }
      },
      "required": [
        "id"
      ]
    }
  },
  "required": [
    "customer"
  ]
}`, `{
  "customer": {
    "id": "1"
  }
}`, `{
  "customer": {}
}`},
		{"nested extras", `{
  "properties": {
    "customer": {
      "type": "object",
      "properties": {
        "id": {
          "type": "string"
        }
      },
      "additionalProperties": false
    }
  }
}`, `{
  "customer": {
    "id": "1"
  }
}`, `{
  "customer": {
    "id": "1",
    "extra": true
  }
}`},
		{"root extras", `{
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "additionalProperties": false
}`, `{
  "id": "1"
}`, `{
  "id": "1",
  "extra": true
}`},
		{"array items", `{
  "properties": {
    "ids": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "uniqueItems": true
    }
  }
}`, `{
  "ids": [
    1,
    2
  ]
}`, `{
  "ids": [
    1,
    "2"
  ]
}`},
		{"oneOf", `{
  "oneOf": [
    {
      "required": [
        "email"
      ]
    },
    {
      "required": [
        "phone"
      ]
    }
  ]
}`, `{
  "email": "x"
}`, `{
  "email": "x",
  "phone": "y"
}`},
		{"allOf", `{
  "allOf": [
    {
      "required": [
        "email"
      ]
    },
    {
      "required": [
        "phone"
      ]
    }
  ]
}`, `{
  "email": "x",
  "phone": "y"
}`, `{
  "email": "x"
}`},
		{"anyOf", `{"anyOf":[{"required":["email"]},{"required":["phone"]}]}`, `{"email":"x","phone":"y"}`, `{}`},
		{"conditional", `{
  "properties": {
    "kind": {
      "type": "string"
    }
  },
  "if": {
    "properties": {
      "kind": {
        "const": "email"
      }
    }
  },
  "then": {
    "required": [
      "email"
    ]
  },
  "else": {
    "required": [
      "phone"
    ]
  }
}`, `{
  "kind": "email",
  "email": "x"
}`, `{
  "kind": "email",
  "phone": "x"
}`},
		{"nullable required", `{
  "properties": {
    "value": {
      "type": [
        "string",
        "null"
      ]
    }
  },
  "required": [
    "value"
  ]
}`, `{
  "value": null
}`, `{}`},
		{"defs", `{
  "$defs": {
    "id": {
      "type": "integer",
      "minimum": 1
    }
  },
  "properties": {
    "id": {
      "$ref": "#/$defs/id"
    }
  }
}`, `{
  "id": 1
}`, `{
  "id": 0
}`},
		{"unicode length", `{"properties":{"name":{"type":"string","minLength":2}}}`, `{"name":"中文"}`, `{"name":"中"}`},
		{"boolean schema", `{"properties":{"forbidden":false}}`, `{}`, `{"forbidden":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewMCPTool(
				&types.MCPService{ID: "svc"},
				&types.MCPTool{Name: "test", InputSchema: json.RawMessage(tc.schema)},
				nil,
				nil,
				0,
			)
			require.NoError(t, tool.ValidateArguments(json.RawMessage(tc.valid)))
			require.Error(t, tool.ValidateArguments(json.RawMessage(tc.invalid)))
			// Exercise the actual execution boundary. The nil manager must never
			// be reached for invalid parameters, even via direct registration.
			r := NewToolRegistry()
			r.RegisterTool(tool)
			result, err := r.ExecuteTool(catalogTestContext(), tool.Name(), json.RawMessage(tc.invalid))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Contains(t, result.Error, "Parameter validation failed")
		})
	}
}

func TestMCPSchemaDoesNotLoadExternalResources(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"type":"object"}`), 0o600))
	for _, ref := range []string{server.URL, "file://" + file} {
		schema := json.RawMessage(fmt.Sprintf(`{"$ref":%q}`, ref))
		tool := NewMCPTool(&types.MCPService{}, &types.MCPTool{InputSchema: schema}, nil, nil, 0)
		require.ErrorContains(t, tool.ValidateArguments(json.RawMessage(`{}`)), "no URLLoader set")
	}
	require.Zero(t, requests.Load())
	tool := NewMCPTool(&types.MCPService{}, &types.MCPTool{InputSchema: json.RawMessage(`{
  "properties": 42
}`)}, nil, nil, 0)
	require.ErrorContains(t, tool.ValidateArguments(json.RawMessage(`{}`)), "schema cannot be validated")
}

func TestMCPSchemaCompilationIsConcurrentSafe(t *testing.T) {
	tool := NewMCPTool(&types.MCPService{}, &types.MCPTool{InputSchema: json.RawMessage(`{
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ]
}`)}, nil, nil, 0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { require.NoError(t, tool.ValidateArguments(json.RawMessage(`{"id":"1"}`))) })
	}
	wg.Wait()
	for _, args := range []string{`null`, `[]`, `{`, `{} {}`} {
		require.Error(t, tool.ValidateArguments(json.RawMessage(args)))
	}
}
