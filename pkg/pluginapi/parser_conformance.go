package pluginapi

import (
	"context"
	"fmt"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

type ParserConformanceReport struct {
	PluginID    string        `json:"plugin_id"`
	HandshakeOK bool          `json:"handshake_ok"`
	HealthOK    bool          `json:"health_ok"`
	ParseOK     bool          `json:"parse_ok"`
	Duration    time.Duration `json:"duration"`
	Errors      []string      `json:"errors,omitempty"`
}

// RunParserConformance is the minimal smoke test for an external parser
// plugin. It verifies the handshake, health endpoint and one Parse call.
func RunParserConformance(ctx context.Context, control PluginControlClient, client ParserPluginClient, request ParserRequest) ParserConformanceReport {
	started := time.Now()
	report := ParserConformanceReport{}
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

	wire, err := EncodeParserRequest(request)
	if err != nil {
		appendError(err)
	} else {
		value, callErr := client.Parse(ctx, wire)
		if callErr == nil {
			var response ParserResponse
			callErr = DecodeParserResponse(value, &response)
			if callErr == nil && response.Error != "" {
				callErr = fmt.Errorf("plugin response: %s", response.Error)
			}
			report.ParseOK = callErr == nil
		}
		appendError(callErr)
	}

	report.Duration = time.Since(started)
	return report
}
