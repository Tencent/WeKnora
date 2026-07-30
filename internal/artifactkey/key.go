// Package artifactkey builds stable, content-addressed keys for reusable
// derived artifacts. Raw source content belongs in digests, never in keys.
package artifactkey

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const encodingVersion = "derived-artifact-key/v1"

type KeyInput struct {
	Kind            string
	TenantScope     string
	InputDigest     string
	ModelID         string
	ModelRevision   string
	PromptVersion   string
	ConfigDigest    string
	ProducerVersion string
}

// canonicalKeyInput intentionally uses an array: its order is part of the v1
// wire format and empty strings are represented explicitly.
type canonicalKeyInput [9]string

func Generate(input KeyInput) string {
	canonical := canonicalKeyInput{
		encodingVersion, input.Kind, input.TenantScope, input.InputDigest,
		input.ModelID, input.ModelRevision, input.PromptVersion,
		input.ConfigDigest, input.ProducerVersion,
	}
	b, _ := json.Marshal(canonical)
	return DigestBytes(b)
}

func DigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func DigestText(value string) string { return DigestBytes([]byte(value)) }

// DigestConfig hashes JSON-compatible configuration using encoding/json's
// deterministic map-key ordering. nil and an empty object remain distinct.
func DigestConfig(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize artifact config: %w", err)
	}
	return DigestBytes(b), nil
}
