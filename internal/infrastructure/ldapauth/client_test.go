package ldapauth

import (
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/require"
)

// buildFilter mirrors the replacement SearchUser performs on the filter
// template. It exists so the escaping contract can be asserted without a live
// directory connection (SearchUser itself needs a *ldap.Conn).
func buildFilter(cfg Config, identifier string) string {
	template := cfg.filterTemplate()
	if !strings.Contains(template, "%s") {
		return template
	}
	return strings.ReplaceAll(template, "%s", ldap.EscapeFilter(identifier))
}

func TestConfigSearchBaseFallsBackToBaseDN(t *testing.T) {
	cfg := Config{BaseDN: "dc=example,dc=com"}
	require.Equal(t, "dc=example,dc=com", cfg.searchBase())

	cfg.UserSearchBase = "  ou=people,dc=example,dc=com  "
	require.Equal(t, "ou=people,dc=example,dc=com", cfg.searchBase(),
		"the user search base wins and is trimmed")

	cfg.UserSearchBase = "   "
	require.Equal(t, "dc=example,dc=com", cfg.searchBase(),
		"a whitespace-only override must not shadow the base DN")
}

func TestConfigFilterTemplateDefaultsToMail(t *testing.T) {
	require.Equal(t, "(mail=%s)", Config{}.filterTemplate())

	cfg := Config{UserFilter: "  (&(objectClass=person)(uid=%s))  "}
	require.Equal(t, "(&(objectClass=person)(uid=%s))", cfg.filterTemplate())

	cfg = Config{UserFilter: "(objectClass=serviceAccount)"}
	require.Equal(t, "(objectClass=serviceAccount)", cfg.filterTemplate(),
		"a filter without %s is kept verbatim")
}

func TestConfigTimeoutUsesDefaultInsteadOfZero(t *testing.T) {
	require.Equal(t, DefaultTimeout, Config{}.timeout())
	require.Equal(t, 3*time.Second, Config{Timeout: 3 * time.Second}.timeout())
	require.Equal(t, DefaultTimeout, Config{Timeout: -1 * time.Second}.timeout())
}

// TestBuildFilterEscapesDirectoryMetacharacters is the regression guard for
// LDAP filter injection: an identifier containing *, (, ), \ or NUL must never
// be able to widen or rewrite the filter.
func TestBuildFilterEscapesDirectoryMetacharacters(t *testing.T) {
	cfg := Config{UserFilter: "(&(objectClass=person)(mail=%s))"}

	for _, tc := range []struct {
		name       string
		identifier string
		want       string
	}{
		{"wildcard star", "*", `\2a`},
		{"open paren", "(", `\28`},
		{"close paren", ")", `\29`},
		{"backslash", `\`, `\5c`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := buildFilter(cfg, tc.identifier)
			require.Contains(t, filter, "(mail="+tc.want+")")
			// The identifier must appear escaped, never raw.
			require.NotContains(t, filter, "(mail="+tc.identifier+")")
		})
	}

	// A classic injection attempt must stay inside the value it was placed in.
	// Only RFC 4515 metacharacters ()*\ and non-ASCII bytes are escaped; '|' is
	// literal inside a value and therefore survives verbatim.
	injected := "*)(|(objectClass=*"
	filter := buildFilter(cfg, injected)
	require.Equal(t, `(&(objectClass=person)(mail=\2a\29\28|\28objectClass=\2a))`, filter)
	require.Equal(t, "(&(objectClass=person)", strings.SplitN(filter, ")(mail=", 2)[0]+")",
		"the injected ')' did not escape the value and close the attribute group")

	// A normal identifier is untouched.
	require.Equal(t, "(&(objectClass=person)(mail=alice@example.com))",
		buildFilter(cfg, "alice@example.com"))

	// Multi-placeholder templates get every occurrence escaped.
	multi := Config{UserFilter: "(&(objectClass=person)(|(mail=%s)(sAMAccountName=%s)(uid=%s)))"}
	got := buildFilter(multi, "a*b")
	require.Equal(t, "(&(objectClass=person)(|(mail=a\\2ab)(sAMAccountName=a\\2ab)(uid=a\\2ab)))", got)
	require.Equal(t, 3, strings.Count(got, `a\2ab`))
}

func TestBuildFilterUsesVerbatimTemplateWhenNoPlaceholder(t *testing.T) {
	cfg := Config{UserFilter: "(objectClass=serviceAccount)"}
	require.Equal(t, "(objectClass=serviceAccount)", buildFilter(cfg, "*)(cn=*"))
}

func TestFormatObjectGUIDRendersCanonicalForm(t *testing.T) {
	// AD stores objectGUID as a 16-byte little-endian blob; the first three
	// groups are byte-swapped when rendered.
	raw := []byte{
		0x0a, 0x0b, 0x0c, 0x0d,
		0x1a, 0x1b,
		0x2a, 0x2b,
		0x3a, 0x3b,
		0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f,
	}
	require.Equal(t, "0d0c0b0a-1b1a-2b2a-3a3b-4a4b4c4d4e4f", formatObjectGUID(raw))

	require.Equal(t, "", formatObjectGUID(nil))
	require.Equal(t, "", formatObjectGUID(make([]byte, 15)))
	require.Equal(t, "", formatObjectGUID(make([]byte, 17)))
}

// TestDetectSubjectPrefersStableIdentifiers covers the attribute precedence a
// deployment depends on: entryUUID (OpenLDAP/389DS) then objectGUID (AD) then
// the DN as a last resort.
func TestDetectSubjectPrefersStableIdentifiers(t *testing.T) {
	t.Run("entryUUID wins", func(t *testing.T) {
		e := ldap.NewEntry("uid=alice,dc=example,dc=com", map[string][]string{
			"entryUUID": {"uuid-1234"},
		})
		entry := &Entry{Attributes: map[string]string{}}
		require.Equal(t, "uuid-1234", detectSubject(e, entry))
		require.Equal(t, "uuid-1234", entry.Attributes["entryUUID"])
	})

	t.Run("objectGUID when no entryUUID", func(t *testing.T) {
		e := ldap.NewEntry("cn=alice,dc=example,dc=com", map[string][]string{})
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{
			Name: "objectGUID",
			ByteValues: [][]byte{{
				0x0a, 0x0b, 0x0c, 0x0d, 0x1a, 0x1b, 0x2a, 0x2b,
				0x3a, 0x3b, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f,
			}},
		})
		entry := &Entry{Attributes: map[string]string{}}
		require.Equal(t, "0d0c0b0a-1b1a-2b2a-3a3b-4a4b4c4d4e4f", detectSubject(e, entry))
		require.Equal(t, "0d0c0b0a-1b1a-2b2a-3a3b-4a4b4c4d4e4f", entry.Attributes["objectGUID"])
	})

	t.Run("malformed objectGUID falls back to DN", func(t *testing.T) {
		e := ldap.NewEntry("cn=alice,dc=example,dc=com", map[string][]string{})
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{
			Name:       "objectGUID",
			ByteValues: [][]byte{[]byte("too-short")},
		})
		entry := &Entry{Attributes: map[string]string{}}
		require.Equal(t, "cn=alice,dc=example,dc=com", detectSubject(e, entry))
		require.NotContains(t, entry.Attributes, "objectGUID")
	})

	t.Run("no stable attribute uses DN", func(t *testing.T) {
		e := ldap.NewEntry("cn=alice,dc=example,dc=com", map[string][]string{"cn": {"alice"}})
		entry := &Entry{Attributes: map[string]string{}}
		require.Equal(t, "cn=alice,dc=example,dc=com", detectSubject(e, entry))
	})
}

// TestVerifyPasswordRejectsBlankPassword asserts the guard runs before any
// network traffic: an empty password must never reach the directory as an
// (accidental) anonymous bind, which some servers accept as success.
func TestVerifyPasswordRejectsBlankPassword(t *testing.T) {
	// A host that is guaranteed not to resolve — if the empty-password guard
	// were removed, the failure would be a dial error, not ErrInvalidCredentials.
	cfg := Config{Host: "127.0.0.1", Port: 1, Timeout: 50 * time.Millisecond}

	for _, pw := range []string{"", "   ", "\t\n"} {
		err := VerifyPassword(cfg, "cn=alice,dc=example,dc=com", pw)
		require.ErrorIs(t, err, ErrInvalidCredentials,
			"blank password must be rejected without dialling the directory")
	}
}

func TestServiceBindRequiresHost(t *testing.T) {
	_, err := ServiceBind(Config{BaseDN: "dc=example,dc=com"})
	require.ErrorIs(t, err, ErrConfigIncomplete)
}

func TestSearchUserRequiresSearchBase(t *testing.T) {
	// A nil connection is safe here: the base-DN check happens first.
	_, err := SearchUser(nil, Config{Host: "ldap.example.com"}, "alice")
	require.ErrorIs(t, err, ErrConfigIncomplete)
}

func TestDetectSubjectAttributeRequiresSearchBase(t *testing.T) {
	_, err := DetectSubjectAttribute(nil, Config{Host: "ldap.example.com"})
	require.ErrorIs(t, err, ErrConfigIncomplete)
}
