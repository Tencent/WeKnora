package pluginapi

import (
	"context"
	"fmt"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

type WebSearchConformanceReport struct {
	PluginID    string        `json:"plugin_id"`
	HandshakeOK bool          `json:"handshake_ok"`
	HealthOK    bool          `json:"health_ok"`
	SearchOK    bool          `json:"search_ok"`
	Duration    time.Duration `json:"duration"`
	Errors      []string      `json:"errors,omitempty"`
}

// RunWebSearchConformance is the SDK-level smoke test for an external search
// plugin. It deliberately uses a caller-provided request so it works for both
// keyless and credentialed providers.
func RunWebSearchConformance(ctx context.Context, control PluginControlClient, client WebSearchPluginClient, request WebSearchRequest) WebSearchConformanceReport {
	started := time.Now()
	report := WebSearchConformanceReport{}
	appendError := func(err error) {
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		}
	}

	handshake, err := control.Handshake(ctx, &pluginproto.HandshakeRequest{})
	if err == nil {
		var value HandshakeResponse
		err = DecodeHandshake(handshake, &value)
		report.PluginID = value.PluginID
		report.HandshakeOK = err == nil && value.ProtocolVersion == ProtocolVersionV1
	}
	appendError(err)
	if !report.HandshakeOK && err == nil {
		appendError(fmt.Errorf("invalid handshake response"))
	}

	health, err := control.Health(ctx, &pluginproto.HealthRequest{})
	if err == nil {
		var value HealthResponse
		err = DecodeHealth(health, &value)
		report.HealthOK = err == nil && value.State != ""
	}
	appendError(err)
	if !report.HealthOK && err == nil {
		appendError(fmt.Errorf("invalid health response"))
	}

	wire, err := EncodeWebSearchRequest(request)
	if err != nil {
		appendError(err)
	} else {
		value, callErr := client.Search(ctx, wire)
		if callErr == nil {
			var response WebSearchResponse
			callErr = DecodeWebSearchResponse(value, &response)
			if callErr == nil && response.Error != "" {
				callErr = fmt.Errorf("plugin response: %s", response.Error)
			}
			report.SearchOK = callErr == nil
		}
		appendError(callErr)
	}

	report.Duration = time.Since(started)
	return report
}
