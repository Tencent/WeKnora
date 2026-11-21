// Package docs exposes the embedded OpenAPI 2.0 specification for Swagger UI
// and runtime operation discovery without duplicating swagger.json in Go code.
package docs

import (
	_ "embed"
	"encoding/json"

	"github.com/swaggo/swag"
)

//go:embed swagger.json
var swaggerJSON string

const (
	defaultVersion     = "1.0"
	defaultHost        = ""
	defaultBasePath    = "/api/v1"
	defaultTitle       = "WeKnora API"
	defaultDescription = "WeKnora 知识库管理系统 API 文档"
)

// Spec wraps swag.Spec so callers can read or override metadata fields while
// serving the embedded swagger.json directly in the common unmodified path.
type Spec struct {
	swag.Spec
}

// ReadDoc returns the OpenAPI JSON document, applying any runtime metadata
// overrides configured on SwaggerInfo without running text/template over
// endpoint descriptions.
func (s *Spec) ReadDoc() string {
	if s.isDefault() {
		return swaggerJSON
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(swaggerJSON), &root); err != nil {
		return swaggerJSON
	}

	if s.Host != "" {
		if raw, err := json.Marshal(s.Host); err == nil {
			root["host"] = raw
		}
	} else {
		delete(root, "host")
	}

	if s.BasePath != "" {
		if raw, err := json.Marshal(s.BasePath); err == nil {
			root["basePath"] = raw
		}
	} else {
		delete(root, "basePath")
	}

	if len(s.Schemes) > 0 {
		if raw, err := json.Marshal(s.Schemes); err == nil {
			root["schemes"] = raw
		}
	} else {
		delete(root, "schemes")
	}

	var info map[string]any
	if rawInfo, ok := root["info"]; ok {
		_ = json.Unmarshal(rawInfo, &info)
	}
	if info == nil {
		info = make(map[string]any, 3)
	}
	info["version"] = s.Version
	info["title"] = s.Title
	info["description"] = s.Description
	if rawInfo, err := json.Marshal(info); err == nil {
		root["info"] = rawInfo
	}

	out, err := json.Marshal(root)
	if err != nil {
		return swaggerJSON
	}
	return string(out)
}

func (s *Spec) isDefault() bool {
	return s.Version == defaultVersion &&
		s.Host == defaultHost &&
		s.BasePath == defaultBasePath &&
		len(s.Schemes) == 0 &&
		s.Title == defaultTitle &&
		s.Description == defaultDescription
}

// SwaggerInfo holds exported Swagger metadata so callers can inspect or
// override Host, BasePath, Schemes, Version, Title, or Description at runtime.
var SwaggerInfo = &Spec{
	Spec: swag.Spec{
		Version:          defaultVersion,
		Host:             defaultHost,
		BasePath:         defaultBasePath,
		Schemes:          []string{},
		Title:            defaultTitle,
		Description:      defaultDescription,
		InfoInstanceName: "swagger",
	},
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
