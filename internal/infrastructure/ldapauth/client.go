// Package ldapauth wraps the subset of LDAP/AD operations WeKnora needs for
// directory-backed login: connect (plain / StartTLS / LDAPS), bind as the
// configured service account, search for a user entry, and re-bind as that
// entry to verify a password.
//
// The package is deliberately transport-only: it knows nothing about WeKnora
// accounts, tenants or provisioning. Everything above it lives in
// application/service/platform_auth_provider.go.
package ldapauth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// DefaultTimeout bounds every directory call when the configuration does not
// specify one. A hanging directory must never hold an HTTP login request open.
const DefaultTimeout = 8 * time.Second

// Config is the resolved, transport-level directory configuration.
type Config struct {
	Host            string
	Port            int
	UseTLS          bool
	StartTLS        bool
	SkipTLSVerify   bool
	BindDN          string
	BindPassword    string
	BaseDN          string
	UserSearchBase  string
	UserFilter      string
	UserNameAttr    string
	UserEmailAttr   string
	UserDisplayAttr string
	Timeout         time.Duration
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// searchBase returns the subtree the user search starts from.
func (c Config) searchBase() string {
	if strings.TrimSpace(c.UserSearchBase) != "" {
		return strings.TrimSpace(c.UserSearchBase)
	}
	return strings.TrimSpace(c.BaseDN)
}

// filterTemplate returns the configured user filter, or a mail-based default.
func (c Config) filterTemplate() string {
	if strings.TrimSpace(c.UserFilter) != "" {
		return strings.TrimSpace(c.UserFilter)
	}
	return "(mail=%s)"
}

// Entry is a matched directory entry with all mapped attributes pre-resolved.
type Entry struct {
	DN         string
	Username   string
	Email      string
	Display    string
	Subject    string
	Attributes map[string]string
}

var (
	// ErrNoUserMatch is returned when the search filter matches no entry.
	ErrNoUserMatch = errors.New("no matching directory entry")
	// ErrInvalidCredentials is returned when the user bind is rejected.
	ErrInvalidCredentials = errors.New("directory rejected the credentials")
	// ErrConfigIncomplete is returned when the host/base DN is missing.
	ErrConfigIncomplete = errors.New("ldap host and base dn are required")
)

// dial opens a connection and leaves it unbound.
func dial(cfg Config) (*ldap.Conn, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, ErrConfigIncomplete
	}
	port := cfg.Port
	if port <= 0 {
		if cfg.UseTLS {
			port = 636
		} else {
			port = 389
		}
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))

	scheme := "ldap"
	if cfg.UseTLS {
		scheme = "ldaps"
	}
	rawURL := scheme + "://" + address

	var opts []ldap.DialOpt
	if cfg.UseTLS {
		opts = append(opts, ldap.DialWithTLSConfig(&tls.Config{
			ServerName:         host,
			InsecureSkipVerify: cfg.SkipTLSVerify,
			MinVersion:         tls.VersionTLS12,
		}))
	}

	conn, err := ldap.DialURL(rawURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("ldap dial %s failed: %w", address, err)
	}
	conn.SetTimeout(cfg.timeout())

	if !cfg.UseTLS && cfg.StartTLS {
		if err := conn.StartTLS(&tls.Config{
			ServerName:         host,
			InsecureSkipVerify: cfg.SkipTLSVerify,
			MinVersion:         tls.VersionTLS12,
		}); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap starttls failed: %w", err)
		}
	}
	return conn, nil
}

// ServiceBind opens a connection and binds with the configured service
// account. An empty BindDN performs an anonymous bind, which is what many
// read-only directories expect.
func ServiceBind(cfg Config) (*ldap.Conn, error) {
	conn, err := dial(cfg)
	if err != nil {
		return nil, err
	}
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ldap service bind failed: %w", err)
	}
	return conn, nil
}

// SearchUser finds the single entry matching `identifier` through the
// configured filter. `%s` in the filter is replaced with the LDAP-escaped
// identifier; a filter without `%s` is used verbatim (useful for
// group-scoped filters combined by the operator).
func SearchUser(conn *ldap.Conn, cfg Config, identifier string) (*Entry, error) {
	base := cfg.searchBase()
	if base == "" {
		return nil, ErrConfigIncomplete
	}

	escaped := ldap.EscapeFilter(identifier)
	template := cfg.filterTemplate()
	var filter string
	if strings.Contains(template, "%s") {
		filter = fmt.Sprintf(template, escaped)
	} else {
		filter = template
	}

	attrs := []string{"*"}
	for _, a := range []string{cfg.UserNameAttr, cfg.UserEmailAttr, cfg.UserDisplayAttr, "entryUUID", "objectGUID", "uid"} {
		if a = strings.TrimSpace(a); a != "" {
			attrs = append(attrs, a)
		}
	}

	req := ldap.NewSearchRequest(
		base,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2, // size limit: we only ever want one entry
		0,
		false,
		filter,
		attrs,
		nil,
	)

	res, err := conn.Search(req)
	if err != nil {
		// A size-limit breach is reported as an error but still carries the
		// entries; treat it as "found, but ambiguous".
		if !strings.Contains(strings.ToLower(err.Error()), "size limit") {
			return nil, err
		}
	}
	if res == nil || len(res.Entries) == 0 {
		return nil, ErrNoUserMatch
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("filter matched %d entries, expected exactly one", len(res.Entries))
	}

	e := res.Entries[0]
	entry := &Entry{DN: e.DN, Attributes: map[string]string{}}

	read := func(attr string) string {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			return ""
		}
		v := strings.TrimSpace(e.GetAttributeValue(attr))
		if v != "" {
			entry.Attributes[attr] = v
		}
		return v
	}

	entry.Username = read(cfg.UserNameAttr)
	entry.Email = read(cfg.UserEmailAttr)
	entry.Display = read(cfg.UserDisplayAttr)
	entry.Subject = detectSubject(e, entry)

	// Aliases operators commonly expect to see in the test panel even when
	// they are not the mapped attribute.
	for _, a := range []string{"cn", "displayName", "mail", "sAMAccountName", "uid"} {
		if _, ok := entry.Attributes[a]; !ok {
			if v := strings.TrimSpace(e.GetAttributeValue(a)); v != "" {
				entry.Attributes[a] = v
			}
		}
	}
	return entry, nil
}

// detectSubject picks the most stable identifier the directory exposes,
// preferring entryUUID (OpenLDAP / 389DS) then objectGUID (Active Directory).
// Falls back to the DN, which is stable in practice for a given entry.
func detectSubject(e *ldap.Entry, entry *Entry) string {
	if v := strings.TrimSpace(e.GetAttributeValue("entryUUID")); v != "" {
		entry.Attributes["entryUUID"] = v
		return v
	}
	if raw := e.GetRawAttributeValue("objectGUID"); len(raw) == 16 {
		guid := formatObjectGUID(raw)
		entry.Attributes["objectGUID"] = guid
		return guid
	}
	return e.DN
}

// formatObjectGUID renders an AD objectGUID (a 16-byte little-endian binary)
// in the canonical 8-4-4-4-12 textual form.
func formatObjectGUID(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[3], b[2], b[1], b[0],
		b[5], b[4],
		b[7], b[6],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

// VerifyPassword opens a fresh connection and binds as the given DN. Using a
// separate connection (rather than rebinding the service connection) keeps the
// service session intact for any follow-up reads and avoids a partially
// authenticated connection being reused.
func VerifyPassword(cfg Config, dn, password string) error {
	if strings.TrimSpace(password) == "" {
		return ErrInvalidCredentials
	}
	conn, err := dial(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.Bind(dn, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("ldap user bind failed: %w", err)
	}
	return nil
}

// DetectSubjectAttribute reports whether the directory exposes entryUUID or
// objectGUID on a sample entry, so the test panel can tell operators which
// attribute carries the stable subject id in this deployment.
func DetectSubjectAttribute(conn *ldap.Conn, cfg Config) (string, error) {
	base := cfg.searchBase()
	if base == "" {
		return "", ErrConfigIncomplete
	}
	req := ldap.NewSearchRequest(
		base,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1, 0, false,
		"(objectClass=*)",
		[]string{"entryUUID", "objectGUID"},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil || res == nil || len(res.Entries) == 0 {
		return "", err
	}
	if v := strings.TrimSpace(res.Entries[0].GetAttributeValue("entryUUID")); v != "" {
		return "entryUUID", nil
	}
	if raw := res.Entries[0].GetRawAttributeValue("objectGUID"); len(raw) == 16 {
		return "objectGUID", nil
	}
	return "", nil
}
