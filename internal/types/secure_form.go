package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"
)

// AgentPromptKindSecureForm asks the user for credentials the backend fills
// into the sandbox browser. Its answer carries a handle, never the values.
const AgentPromptKindSecureForm = "secure_form"

// Limits on a secure form. The model writes the request, so its size is
// bounded to keep the card readable and the stored row small.
const (
	SecureFormMaxTitleLen    = 200
	SecureFormMaxDomains     = 5
	SecureFormMaxExtra       = 12
	SecureFormMaxLabelLen    = 200
	SecureFormMaxUsernameLen = 256
	SecureFormMaxValueBytes  = 4096
)

const (
	SecureFormKindLogin     = "login"
	SecureFormFieldUsername = "username"
	SecureFormFieldPassword = "password"
)

// Types of the values a secure form collects.
const (
	SecretFieldText        = "text"
	SecretFieldEmail       = "email"
	SecretFieldPassword    = "password"
	SecretFieldOneTimeCode = "one_time_code"
	SecretFieldNumber      = "number"
)

// Per-field outcomes of browser_fill_secret.
const (
	SecretFillFilled                = "filled"
	SecretFillHostMismatch          = "host_mismatch"
	SecretFillInsecureOrigin        = "insecure_origin"
	SecretFillRefNotFound           = "ref_not_found"
	SecretFillNotEditable           = "not_editable"
	SecretFillNotPasswordField      = "not_password_field"
	SecretFillPageChanged           = "page_changed"
	SecretFillVerifyFailed          = "verify_failed"
	SecretFillHandleExpired         = "handle_expired"
	SecretFillCredentialUnavailable = "credential_unavailable"
)

var (
	// ErrSecretHandleNotFound covers a handle that expired, was used up, or
	// belongs to another session or user: the caller cannot tell them apart.
	ErrSecretHandleNotFound = errors.New("secret handle not found or expired")
	// ErrCredentialUnavailable means the saved credential behind a handle was
	// deleted or can no longer be decrypted.
	ErrCredentialUnavailable = errors.New("saved credential is unavailable")

	errInvalidHost = errors.New("must be a host name such as accounts.google.com, without scheme, path, port wildcard or credentials")
)

var (
	secureFormNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	secretHandlePattern   = regexp.MustCompile(`^cred_[1-9][0-9]{0,9}$`)
	hostLabelPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// SecureFormExtraField is one non-login input of a secure form.
type SecureFormExtraField struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// SecureFormPrefill holds non-secret values the card shows pre-filled.
type SecureFormPrefill struct {
	Username string `json:"username,omitempty"`
}

// SecureFormPromptRequest is the Request of an AgentPromptKindSecureForm.
type SecureFormPromptRequest struct {
	Title     string                 `json:"title"`
	Kind      string                 `json:"kind,omitempty"`
	Site      string                 `json:"site"`
	Domains   []string               `json:"domains,omitempty"`
	Parts     []string               `json:"parts,omitempty"`
	Prefill   *SecureFormPrefill     `json:"prefill,omitempty"`
	Extra     []SecureFormExtraField `json:"extra,omitempty"`
	AllowSave *bool                  `json:"allow_save,omitempty"`
	// ObservedOrigin is the browser's real top-level origin, read by the
	// server before the card is shown; empty means it could not be read.
	// The tool overwrites whatever the model put here.
	ObservedOrigin string `json:"observed_origin,omitempty"`
}

// SecureFormPromptResponse is the Response of an AgentPromptKindSecureForm.
// Only the server writes it; it never carries a value the user typed.
type SecureFormPromptResponse struct {
	Declined bool     `json:"declined,omitempty"`
	Handle   string   `json:"handle,omitempty"`
	Fields   []string `json:"fields,omitempty"`
	Username string   `json:"username,omitempty"`
	Saved    bool     `json:"saved,omitempty"`
}

// SecretField is one value behind a handle.
type SecretField struct {
	Name  string
	Type  string
	Value string
}

// ResolvedSecret is what a handle unlocks for one fill.
type ResolvedSecret struct {
	Domains     []string
	Fields      []SecretField
	VaultItemID string
}

// ValidSecretHandle reports whether h has the cred_N shape.
func ValidSecretHandle(h string) bool { return secretHandlePattern.MatchString(h) }

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NormalizeHost lowercases a host name or IP and drops a port and a trailing
// dot. URLs, paths and wildcards are rejected.
func NormalizeHost(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", errors.New("host is required")
	}
	if strings.ContainsAny(s, "/*@ \t") {
		return "", errInvalidHost
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), ".")
	if ip := net.ParseIP(s); ip != nil {
		return ip.String(), nil
	}
	if len(s) > 253 {
		return "", errInvalidHost
	}
	labels := strings.Split(s, ".")
	last := labels[len(labels)-1]
	if isAllDigits(last) || strings.HasPrefix(last, "0x") {
		return "", errInvalidHost
	}
	for _, label := range labels {
		if !hostLabelPattern.MatchString(label) {
			return "", errInvalidHost
		}
	}
	return s, nil
}

// NormalizeDomains returns site followed by the other distinct hosts.
func NormalizeDomains(site string, domains []string) ([]string, error) {
	out := []string{site}
	seen := map[string]bool{site: true}
	for _, d := range domains {
		h, err := NormalizeHost(d)
		if err != nil {
			return nil, err
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if len(out) > SecureFormMaxDomains {
		return nil, fmt.Errorf("at most %d domains", SecureFormMaxDomains)
	}
	return out, nil
}

// CheckSameSite requires every domain to share the registrable domain
// (eTLD+1, private suffixes included) of site. An IP or a name without one,
// such as an intranet host, only admits itself.
func CheckSameSite(site string, domains []string) error {
	base, err := publicsuffix.EffectiveTLDPlusOne(site)
	if err != nil || net.ParseIP(site) != nil {
		base = ""
	}
	for _, d := range domains {
		if d == site {
			continue
		}
		if base == "" || (d != base && !strings.HasSuffix(d, "."+base)) {
			return fmt.Errorf("domain %s is not part of %s", d, site)
		}
	}
	return nil
}

// IntersectDomains keeps the hosts of a that b also allows, in a's order.
func IntersectDomains(a, b []string) []string {
	allowed := make(map[string]bool, len(b))
	for _, h := range b {
		allowed[h] = true
	}
	var out []string
	for _, h := range a {
		if allowed[h] {
			out = append(out, h)
		}
	}
	return out
}

// Validate normalizes the request and checks it against the limits.
func (r *SecureFormPromptRequest) Validate() error {
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" {
		return errors.New("title is required")
	}
	if utf8.RuneCountInString(r.Title) > SecureFormMaxTitleLen {
		return fmt.Errorf("title exceeds %d characters", SecureFormMaxTitleLen)
	}
	switch r.Kind {
	case "", SecureFormKindLogin:
	default:
		return fmt.Errorf("unsupported kind %q", r.Kind)
	}
	site, err := NormalizeHost(r.Site)
	if err != nil {
		return fmt.Errorf("site: %w", err)
	}
	r.Site = site
	if r.Domains, err = NormalizeDomains(site, r.Domains); err != nil {
		return err
	}
	if err := CheckSameSite(site, r.Domains); err != nil {
		return err
	}
	if r.Kind == SecureFormKindLogin {
		if err := r.normalizeParts(); err != nil {
			return err
		}
	} else {
		if len(r.Parts) > 0 || r.Prefill != nil {
			return errors.New(`parts and prefill need kind "login"`)
		}
		if len(r.Extra) == 0 {
			return errors.New(`a form without kind "login" needs extra fields`)
		}
	}
	if r.Prefill != nil {
		r.Prefill.Username = strings.TrimSpace(r.Prefill.Username)
		if !r.HasPart(SecureFormFieldUsername) {
			return errors.New("prefill.username needs the username part")
		}
		if utf8.RuneCountInString(r.Prefill.Username) > SecureFormMaxUsernameLen {
			return fmt.Errorf("prefill.username exceeds %d characters", SecureFormMaxUsernameLen)
		}
	}
	return r.validateExtra()
}

func (r *SecureFormPromptRequest) normalizeParts() error {
	if len(r.Parts) == 0 {
		r.Parts = []string{SecureFormFieldUsername, SecureFormFieldPassword}
	}
	seen := map[string]bool{}
	for _, p := range r.Parts {
		if p != SecureFormFieldUsername && p != SecureFormFieldPassword {
			return fmt.Errorf("unsupported part %q", p)
		}
		if seen[p] {
			return fmt.Errorf("duplicate part %q", p)
		}
		seen[p] = true
	}
	parts := make([]string, 0, 2)
	for _, p := range []string{SecureFormFieldUsername, SecureFormFieldPassword} {
		if seen[p] {
			parts = append(parts, p)
		}
	}
	r.Parts = parts
	return nil
}

func (r *SecureFormPromptRequest) validateExtra() error {
	if len(r.Extra) > SecureFormMaxExtra {
		return fmt.Errorf("at most %d extra fields", SecureFormMaxExtra)
	}
	names := map[string]bool{SecureFormFieldUsername: true, SecureFormFieldPassword: true}
	for i := range r.Extra {
		f := &r.Extra[i]
		f.Name = strings.TrimSpace(f.Name)
		if !secureFormNamePattern.MatchString(f.Name) {
			return fmt.Errorf("extra field %d needs a lowercase name of letters, digits and _", i)
		}
		if names[f.Name] {
			return fmt.Errorf("duplicate or reserved field name %q", f.Name)
		}
		names[f.Name] = true
		if utf8.RuneCountInString(f.Label) > SecureFormMaxLabelLen {
			return fmt.Errorf("label of field %q exceeds %d characters", f.Name, SecureFormMaxLabelLen)
		}
		switch f.Type {
		case SecretFieldText, SecretFieldEmail, SecretFieldPassword, SecretFieldOneTimeCode, SecretFieldNumber:
		default:
			return fmt.Errorf("field %q has unsupported type %q", f.Name, f.Type)
		}
	}
	return nil
}

// HasPart reports whether the login asks for part.
func (r *SecureFormPromptRequest) HasPart(part string) bool {
	for _, p := range r.Parts {
		if p == part {
			return true
		}
	}
	return false
}

// AllowsSave reports whether the card may offer saving to the vault.
func (r *SecureFormPromptRequest) AllowsSave() bool {
	return r.Kind == SecureFormKindLogin && r.HasPart(SecureFormFieldPassword) &&
		(r.AllowSave == nil || *r.AllowSave)
}

// ExtraNames lists the extra fields in request order.
func (r *SecureFormPromptRequest) ExtraNames() []string {
	names := make([]string, 0, len(r.Extra))
	for _, f := range r.Extra {
		names = append(names, f.Name)
	}
	return names
}

// FieldNames lists every field: login parts first, then extra fields.
func (r *SecureFormPromptRequest) FieldNames() []string {
	return append(append([]string{}, r.Parts...), r.ExtraNames()...)
}

// FieldType returns the value type of a field.
func (r *SecureFormPromptRequest) FieldType(name string) (string, bool) {
	switch {
	case name == SecureFormFieldUsername && r.HasPart(name):
		return SecretFieldText, true
	case name == SecureFormFieldPassword && r.HasPart(name):
		return SecretFieldPassword, true
	}
	for _, f := range r.Extra {
		if f.Name == name {
			return f.Type, true
		}
	}
	return "", false
}

// FieldRequired reports whether the user must fill a field. Login parts are
// always required: the agent asked for exactly those.
func (r *SecureFormPromptRequest) FieldRequired(name string) bool {
	if r.HasPart(name) {
		return true
	}
	for _, f := range r.Extra {
		if f.Name == name {
			return f.Required
		}
	}
	return false
}

// ValidateSecureFormResponse is the broker validator of
// AgentPromptKindSecureForm. It accepts only the server-built shape, so a raw
// answer carrying values can never be stored on the prompt row.
func ValidateSecureFormResponse(request, response json.RawMessage) error {
	var q SecureFormPromptRequest
	if err := json.Unmarshal(request, &q); err != nil {
		return fmt.Errorf("unreadable secure form request: %w", err)
	}
	var a SecureFormPromptResponse
	dec := json.NewDecoder(bytes.NewReader(response))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return errors.New("unreadable secure form response")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unreadable secure form response")
	}
	if a.Declined {
		if a.Handle != "" || len(a.Fields) > 0 || a.Username != "" || a.Saved {
			return errors.New("a declined form carries no content")
		}
		return nil
	}
	if !ValidSecretHandle(a.Handle) {
		return errors.New("invalid handle")
	}
	if len(a.Fields) == 0 {
		return errors.New("fields are required")
	}
	allowed := map[string]bool{}
	for _, name := range q.FieldNames() {
		allowed[name] = true
	}
	for _, f := range a.Fields {
		if !allowed[f] {
			return errors.New("fields name a field the form does not have")
		}
	}
	if utf8.RuneCountInString(a.Username) > SecureFormMaxUsernameLen {
		return fmt.Errorf("username exceeds %d characters", SecureFormMaxUsernameLen)
	}
	return nil
}
