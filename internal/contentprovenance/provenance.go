// Package contentprovenance authenticates content-pipeline Agent requests.
package contentprovenance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	Version         = 1
	EnvelopeHeader  = "X-WeKnora-Content-Provenance"
	SignatureHeader = "X-WeKnora-Content-Signature"
	maxEnvelopeSize = 16 * 1024
)

var (
	ErrMissingSecret     = errors.New("content pipeline provenance secret is not configured")
	ErrInvalidEnvelope   = errors.New("invalid content pipeline provenance envelope")
	ErrInvalidSignature  = errors.New("invalid content pipeline provenance signature")
	ErrMismatchedRequest = errors.New("content pipeline provenance does not match request")
)

// Job identifies the persisted custom-backend task that owns an Agent run.
type Job struct {
	TaskID               string
	VideoID              string
	TranscriptGeneration string
	JobType              string
}

// Envelope binds a persisted production task to one exact Agent request.
type Envelope struct {
	Version              int      `json:"version"`
	SessionID            string   `json:"session_id"`
	TaskID               string   `json:"task_id"`
	VideoID              string   `json:"video_id"`
	TranscriptGeneration string   `json:"transcript_generation"`
	JobType              string   `json:"job_type"`
	AgentID              string   `json:"agent_id"`
	SkillName            string   `json:"skill_name"`
	QuerySHA256          string   `json:"query_sha256"`
	KnowledgeBaseIDs     []string `json:"knowledge_base_ids"`
	KnowledgeIDs         []string `json:"knowledge_ids"`
}

// NewEnvelope creates the canonical request binding signed by custom-backend.
func NewEnvelope(
	job Job,
	sessionID, agentID, skillName, query string,
	knowledgeBaseIDs, knowledgeIDs []string,
) Envelope {
	return Envelope{
		Version:              Version,
		SessionID:            strings.TrimSpace(sessionID),
		TaskID:               strings.TrimSpace(job.TaskID),
		VideoID:              strings.TrimSpace(job.VideoID),
		TranscriptGeneration: strings.TrimSpace(job.TranscriptGeneration),
		JobType:              strings.TrimSpace(job.JobType),
		AgentID:              strings.TrimSpace(agentID),
		SkillName:            strings.TrimSpace(skillName),
		QuerySHA256:          hashQuery(query),
		KnowledgeBaseIDs:     canonicalIDs(knowledgeBaseIDs),
		KnowledgeIDs:         canonicalIDs(knowledgeIDs),
	}
}

// Sign returns a transport-safe envelope and its HMAC-SHA256 signature.
func Sign(secret string, envelope Envelope) (string, string, error) {
	if secret == "" {
		return "", "", ErrMissingSecret
	}
	if err := envelope.validate(); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", "", fmt.Errorf("marshal provenance envelope: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded, signature(secret, encoded), nil
}

// Verify authenticates and decodes an envelope without trusting its fields.
func Verify(secret, encoded, signed string) (Envelope, error) {
	var envelope Envelope
	if secret == "" {
		return envelope, ErrMissingSecret
	}
	if encoded == "" || signed == "" || len(encoded) > maxEnvelopeSize {
		return envelope, ErrInvalidEnvelope
	}
	provided, err := hex.DecodeString(signed)
	if err != nil || len(provided) != sha256.Size {
		return envelope, ErrInvalidSignature
	}
	expected, _ := hex.DecodeString(signature(secret, encoded))
	if !hmac.Equal(provided, expected) {
		return envelope, ErrInvalidSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > maxEnvelopeSize {
		return envelope, ErrInvalidEnvelope
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Envelope{}, ErrInvalidEnvelope
	}
	if err := envelope.validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// MatchesRequest checks every caller-controlled field covered by the signature.
func (e Envelope) MatchesRequest(
	sessionID, agentID, query string,
	skillNames, knowledgeBaseIDs, knowledgeIDs []string,
) bool {
	return e.SessionID == strings.TrimSpace(sessionID) &&
		e.AgentID == strings.TrimSpace(agentID) &&
		e.QuerySHA256 == hashQuery(query) &&
		slices.Equal([]string{e.SkillName}, canonicalIDs(skillNames)) &&
		slices.Equal(e.KnowledgeBaseIDs, canonicalIDs(knowledgeBaseIDs)) &&
		slices.Equal(e.KnowledgeIDs, canonicalIDs(knowledgeIDs))
}

func (e Envelope) validate() error {
	if e.Version != Version || emptyOrUnsafe(
		e.SessionID, e.TaskID, e.VideoID, e.TranscriptGeneration, e.JobType,
		e.AgentID, e.SkillName, e.QuerySHA256,
	) || len(e.QuerySHA256) != sha256.Size*2 {
		return ErrInvalidEnvelope
	}
	if !slices.Equal(e.KnowledgeBaseIDs, canonicalIDs(e.KnowledgeBaseIDs)) ||
		!slices.Equal(e.KnowledgeIDs, canonicalIDs(e.KnowledgeIDs)) {
		return ErrInvalidEnvelope
	}
	return nil
}

func emptyOrUnsafe(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
			return true
		}
	}
	return false
}

func canonicalIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func hashQuery(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

func signature(secret, encoded string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return hex.EncodeToString(mac.Sum(nil))
}
