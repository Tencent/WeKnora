// Package artifact implements content-addressed processing artifacts and the
// stable identities used to reconcile them into live knowledge state.
package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const CanonicalJSONVersion = "weknora.canonical-json.v1"

// DirectInput identifies one ordered direct dependency. Downstream stages use
// an upstream OutputDigest here, not the upstream stage's entire configuration.
type DirectInput struct {
	Role   string `json:"role"`
	Digest string `json:"digest"`
}

// ProcessorIdentity contains only effective, non-secret provider identity.
// Callers must render the exact provider request separately in KeyMaterial.
type ProcessorIdentity struct {
	ModelID          string         `json:"model_id,omitempty"`
	ModelName        string         `json:"model_name,omitempty"`
	Source           string         `json:"source,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	EndpointIdentity string         `json:"endpoint_identity,omitempty"`
	Revision         string         `json:"revision,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
}

// KeyMaterial is the versioned canonical envelope hashed into an artifact key.
// DirectInputs is deliberately ordered.
type KeyMaterial struct {
	KeyVersion           uint16            `json:"key_version"`
	Stage                string            `json:"stage"`
	DirectInputs         []DirectInput     `json:"direct_inputs"`
	Processor            ProcessorIdentity `json:"processor"`
	RenderedRequest      any               `json:"rendered_request"`
	Options              any               `json:"options"`
	CanonicalizerVersion string            `json:"canonicalizer_version"`
	OutputSchemaVersion  string            `json:"output_schema_version"`
}

// Key is the complete result of canonical key construction.
type Key struct {
	Lookup          types.ProcessingArtifactLookup
	ProcessorDigest string
	OutputSchema    string
	Canonical       []byte
}

// BuildKey hashes the exact canonical envelope without altering provider
// inputs. Tenant identity is enforced by the database key, not mixed into the
// reusable content digest.
func BuildKey(tenantID uint64, material KeyMaterial) (Key, error) {
	lookup := types.ProcessingArtifactLookup{
		TenantID:    tenantID,
		Stage:       material.Stage,
		KeyVersion:  material.KeyVersion,
		ArtifactKey: strings.Repeat("0", sha256.Size*2),
	}
	if err := lookup.Validate(); err != nil {
		return Key{}, err
	}
	if len(material.DirectInputs) == 0 {
		return Key{}, errors.New("artifact direct inputs must not be empty")
	}
	for index, input := range material.DirectInputs {
		if strings.TrimSpace(input.Role) == "" {
			return Key{}, fmt.Errorf("artifact direct input %d role must not be empty", index)
		}
		if !isSHA256(input.Digest) {
			return Key{}, fmt.Errorf("artifact direct input %d digest must be 64 lowercase hex characters", index)
		}
	}
	if material.CanonicalizerVersion == "" {
		return Key{}, errors.New("artifact canonicalizer version must not be empty")
	}
	if material.OutputSchemaVersion == "" {
		return Key{}, errors.New("artifact output schema version must not be empty")
	}
	if err := validateEndpointIdentity(material.Processor.EndpointIdentity); err != nil {
		return Key{}, err
	}
	if err := RejectSecretFields(material); err != nil {
		return Key{}, err
	}

	processorCanonical, err := CanonicalJSON(material.Processor)
	if err != nil {
		return Key{}, fmt.Errorf("canonicalize artifact processor identity: %w", err)
	}
	canonical, err := CanonicalJSON(material)
	if err != nil {
		return Key{}, fmt.Errorf("canonicalize artifact key material: %w", err)
	}
	processorDigest := SHA256Hex(processorCanonical)
	lookup.ArtifactKey = SHA256Hex(canonical)
	return Key{
		Lookup:          lookup,
		ProcessorDigest: processorDigest,
		OutputSchema:    material.OutputSchemaVersion,
		Canonical:       canonical,
	}, nil
}

// CanonicalJSON returns stable JSON with sorted object keys, preserved array
// order, no insignificant whitespace and normalized finite numbers.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, errors.New("canonical JSON contains a trailing value")
	}

	var result bytes.Buffer
	if err := writeCanonicalJSON(&result, decoded); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func writeCanonicalJSON(result *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		result.WriteString("null")
	case bool:
		if typed {
			result.WriteString("true")
		} else {
			result.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(typed)
		result.Write(encoded)
	case json.Number:
		number, err := canonicalNumber(typed.String())
		if err != nil {
			return err
		}
		result.WriteString(number)
	case []any:
		result.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				result.WriteByte(',')
			}
			if err := writeCanonicalJSON(result, item); err != nil {
				return err
			}
		}
		result.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				result.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			result.Write(encoded)
			result.WriteByte(':')
			if err := writeCanonicalJSON(result, typed[key]); err != nil {
				return err
			}
		}
		result.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", value)
	}
	return nil
}

func canonicalNumber(raw string) (string, error) {
	if !strings.ContainsAny(raw, ".eE") {
		if raw == "-0" {
			return "0", nil
		}
		return raw, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid canonical JSON number %q", raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// RejectSecretFields prevents credentials from influencing cache identity.
func RejectSecretFields(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	return walkSecretFields(decoded, "")
}

func walkSecretFields(value any, path string) error {
	switch typed := value.(type) {
	case []any:
		for index, item := range typed {
			if err := walkSecretFields(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range typed {
			normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
			if _, secret := secretFieldNames[normalized]; secret {
				return fmt.Errorf("secret field %q must not enter artifact key material", joinJSONPath(path, key))
			}
			if err := walkSecretFields(item, joinJSONPath(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}

var secretFieldNames = map[string]struct{}{
	"apikey":        {},
	"authorization": {},
	"accesstoken":   {},
	"refreshtoken":  {},
	"clientsecret":  {},
	"cookie":        {},
	"password":      {},
	"secret":        {},
	"signature":     {},
	"xamzsignature": {},
}

func joinJSONPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func validateEndpointIdentity(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.TrimSpace(raw) != raw {
		return errors.New("artifact endpoint identity must not have surrounding whitespace")
	}
	if !strings.Contains(raw, "://") {
		return nil
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return errors.New("artifact endpoint identity is not a valid absolute URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("artifact endpoint identity must not contain credentials, query parameters or fragments")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func SHA256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
