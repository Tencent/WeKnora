package pluginapi

import (
	"context"
	"fmt"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

type ConformanceReport struct {
	PluginID      string        `json:"plugin_id"`
	HandshakeOK   bool          `json:"handshake_ok"`
	HealthOK      bool          `json:"health_ok"`
	ValidateOK    bool          `json:"validate_ok"`
	FullSyncOK    bool          `json:"full_sync_ok"`
	IncrementalOK bool          `json:"incremental_ok"`
	Duration      time.Duration `json:"duration"`
	Errors        []string      `json:"errors,omitempty"`
}

// RunDataSourceConformance is the repeatable smoke test a third-party plugin
// author can run without importing WeKnora internals. It checks the protocol
// handshake and that the core datasource operations return valid envelopes.
func RunDataSourceConformance(ctx context.Context, control PluginControlClient, client DataSourcePluginClient, request Request) ConformanceReport {
	started := time.Now()
	report := ConformanceReport{}
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
		report.HandshakeOK = err == nil && value.ProtocolVersion == "v1"
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
	wire, err := EncodeRequest(request)
	if err != nil {
		appendError(err)
	} else {
		value, callErr := client.Validate(ctx, wire)
		if callErr == nil {
			var response Response
			callErr = DecodeResponse(value, &response)
			if callErr == nil {
				callErr = responseError(response)
			}
			report.ValidateOK = callErr == nil
		}
		appendError(callErr)
		value, callErr = client.FetchAll(ctx, wire)
		if callErr == nil {
			var response Response
			callErr = DecodeResponse(value, &response)
			if callErr == nil {
				callErr = responseError(response)
			}
			report.FullSyncOK = callErr == nil
		}
		appendError(callErr)
		value, callErr = client.FetchIncremental(ctx, wire)
		if callErr == nil {
			var response Response
			callErr = DecodeResponse(value, &response)
			if callErr == nil {
				callErr = responseError(response)
			}
			report.IncrementalOK = callErr == nil
		}
		appendError(callErr)
	}
	report.Duration = time.Since(started)
	return report
}

func responseError(response Response) error {
	if response.Error != "" {
		return fmt.Errorf("plugin response: %s", response.Error)
	}
	return nil
}
