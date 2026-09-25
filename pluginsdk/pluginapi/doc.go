// Package pluginapi is version 1 of the WeKnora extension protocol: the
// messages WeKnora and a code plugin exchange over HTTP. It has no
// dependencies so that plugins and WeKnora share one definition; openapi.yaml
// in this directory describes the same protocol for other languages.
//
// Every call is a POST whose body is an Envelope and whose successful answer
// is {"output": ...}. Streaming calls answer with NDJSON Events instead. A
// failed call answers with a non-2xx status and an ErrorBody. Within a major
// version the protocol only grows: new optional fields and new endpoints.
// Both sides ignore fields they do not know.
package pluginapi

// ProtocolVersion is the major version this package speaks; it travels in
// the ProtocolHeader of every request.
const ProtocolVersion = "1"

// APIVersion is the manifest apiVersion of plugins speaking this protocol.
const APIVersion = "weknora.plugin/v1"

// Request headers.
const (
	ProtocolHeader  = "X-WeKnora-Protocol"
	RequestIDHeader = "X-Request-Id"
	// SignatureHeader and TimestampHeader authenticate calls to remote
	// plugins: hex HMAC-SHA256 over "<timestamp>.<body>" with the shared
	// secret. Host plugins get a bearer token instead.
	SignatureHeader = "X-WeKnora-Signature"
	TimestampHeader = "X-WeKnora-Timestamp"
)

// NDJSONContentType is the content type of streaming answers.
const NDJSONContentType = "application/x-ndjson"
