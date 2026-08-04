package langfuse

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newExporter builds an OTLP/HTTP trace exporter pointed at the
// Langfuse v3+ / LiteFuse OTel endpoint (POST /api/public/otel/v1/traces).
//
// Auth is HTTP Basic (public_key:secret_key). The x-langfuse-ingestion-version
// header is the gate opt-in that LiteFuse/Langfuse v3 require for the OTel
// direct-write path (verified against directWriteHelpers.ts — without it the
// server returns 400 "requires Python SDK >= 4.0.0"). The x-langfuse-sdk-*
// markers are sent for parity; WeKnora is a Go client, not the Python SDK.
func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	endpoint := strings.TrimRight(cfg.Host, "/") + "/api/public/otel/v1/traces"
	creds := cfg.PublicKey + ":" + cfg.SecretKey
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                "Basic " + base64.StdEncoding.EncodeToString([]byte(creds)),
			"x-langfuse-ingestion-version": "4",
			"x-langfuse-sdk-name":          "python",
			"x-langfuse-sdk-version":       langfuseScopeVersion,
		}),
	}
	if cfg.RequestTimeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(cfg.RequestTimeout))
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("langfuse: build otlp exporter: %w", err)
	}
	return exp, nil
}
