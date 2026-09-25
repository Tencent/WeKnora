package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ConnectorConfig is a data source's instance configuration.
type ConnectorConfig struct {
	Credentials map[string]any `json:"credentials"`
	Settings    map[string]any `json:"settings"`
	ResourceIDs []string       `json:"resourceIds"`
}

// Connector is a data source connector.
type Connector interface {
	// Validate checks the configuration, typically by calling the source.
	// Return pluginapi.InvalidConfig to point at fields.
	Validate(ctx context.Context, call *Call, cfg ConnectorConfig) error
	// ListResources lists what can be synced under parentID ("" = top).
	ListResources(ctx context.Context, call *Call, cfg ConnectorConfig, parentID string) ([]pluginapi.Resource, error)
	// Fetch streams items changed since cursor (nil = everything) and
	// returns the cursor for the next sync. Checkpoint at page boundaries
	// so an interrupted sync resumes.
	Fetch(
		ctx context.Context,
		call *Call,
		cfg ConnectorConfig,
		in pluginapi.FetchInput,
		s *Stream,
	) (*pluginapi.Cursor, error)
}

// AncestorResolver is implemented by connectors whose resource tree loads
// lazily; see pluginapi.ResolveAncestorsInput. Others answer with nothing.
type AncestorResolver interface {
	ResolveAncestors(ctx context.Context, call *Call, cfg ConnectorConfig, resourceIDs []string) ([]string, error)
}

// Connector registers connector id (contributes.connectors[].id).
func (p *Plugin) Connector(id string, c Connector) { p.connectors[id] = c }

func (p *Plugin) connector(w http.ResponseWriter, r *http.Request) (Connector, bool) {
	c, ok := p.connectors[r.PathValue("id")]
	if !ok {
		writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no connector %q", r.PathValue("id")))
	}
	return c, ok
}

func connectorConfig(call *Call) (ConnectorConfig, error) {
	var cfg ConnectorConfig
	if err := call.DecodeInstance(&cfg); err != nil {
		return cfg, pluginapi.Errorf(pluginapi.CodeBadRequest, "decode connector config: %v", err)
	}
	return cfg, nil
}

func (p *Plugin) routeConnectors(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/connectors/{id}/validate", func(w http.ResponseWriter, r *http.Request) {
		c, ok := p.connector(w, r)
		if !ok {
			return
		}
		p.unary(func(ctx context.Context, call *Call, _ json.RawMessage) (any, error) {
			cfg, err := connectorConfig(call)
			if err != nil {
				return nil, err
			}
			return struct{}{}, c.Validate(ctx, call, cfg)
		})(w, r)
	})
	mux.HandleFunc("POST /v1/connectors/{id}/list-resources", func(w http.ResponseWriter, r *http.Request) {
		c, ok := p.connector(w, r)
		if !ok {
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.ListResourcesInput](raw)
			if err != nil {
				return nil, err
			}
			cfg, err := connectorConfig(call)
			if err != nil {
				return nil, err
			}
			res, err := c.ListResources(ctx, call, cfg, in.ParentID)
			if err != nil {
				return nil, err
			}
			if res == nil {
				res = []pluginapi.Resource{}
			}
			return pluginapi.ListResourcesOutput{Resources: res}, nil
		})(w, r)
	})
	mux.HandleFunc("POST /v1/connectors/{id}/resolve-ancestors", func(w http.ResponseWriter, r *http.Request) {
		c, ok := p.connector(w, r)
		if !ok {
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.ResolveAncestorsInput](raw)
			if err != nil {
				return nil, err
			}
			out := pluginapi.ResolveAncestorsOutput{Ancestors: []string{}}
			resolver, ok := c.(AncestorResolver)
			if !ok {
				return out, nil
			}
			cfg, err := connectorConfig(call)
			if err != nil {
				return nil, err
			}
			ids, err := resolver.ResolveAncestors(ctx, call, cfg, in.ResourceIDs)
			if err != nil {
				return nil, err
			}
			if ids != nil {
				out.Ancestors = ids
			}
			return out, nil
		})(w, r)
	})
	mux.HandleFunc("POST /v1/connectors/{id}/fetch", func(w http.ResponseWriter, r *http.Request) {
		c, ok := p.connector(w, r)
		if !ok {
			return
		}
		env, err := readEnvelope(r)
		if err != nil {
			writeError(w, err)
			return
		}
		in, err := decodeInput[pluginapi.FetchInput](env.Input)
		if err != nil {
			writeError(w, err)
			return
		}
		call := &Call{Context: env.Context, Config: env.Config}
		cfg, err := connectorConfig(call)
		if err != nil {
			writeError(w, err)
			return
		}
		ctx, cancel := callContext(r.Context(), env)
		defer cancel()
		s := newStream(w)
		cursor, err := c.Fetch(ctx, call, cfg, in, s)
		if err != nil {
			s.fail(err)
			return
		}
		s.end(cursor)
	})
}
