package allocation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const sid = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeSession, "session": ModeSession, " user ": ModeUser} {
		got, err := ParseMode(in)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	_, err := ParseMode("machine")
	require.Error(t, err)
}

func TestDecideMatrix(t *testing.T) {
	web := Owner{TenantID: 7, ID: "u-1", Kind: KindWebUser, Source: SourceWeb}
	ext := Owner{TenantID: 7, ID: "api_external_user:7:alice", Kind: KindAPIExternalUser, Source: SourceAPISignedToken}
	base := DecideInput{ConfigMode: ModeUser, Purpose: PurposeChat, SessionID: sid, UserModeAvailable: true}
	cases := []struct {
		name   string
		mutate func(*DecideInput)
		mode   Mode
		reason FallbackReason
	}{
		{"web user shares", func(in *DecideInput) { in.Owner = web }, ModeUser, ReasonNone},
		{"signed external user shares", func(in *DecideInput) { in.Owner = ext }, ModeUser, ReasonNone},
		{"direct external user shares", func(in *DecideInput) {
			in.Owner = ext
			in.Owner.Source = SourceAPIDirectHeader
		}, ModeUser, ReasonNone},
		{"session config never shares", func(in *DecideInput) {
			in.Owner = web
			in.ConfigMode = ModeSession
		}, ModeSession, ReasonNone},
		{"maintenance first", func(in *DecideInput) {
			in.Owner = web
			in.Purpose = PurposeMaintenance
		}, ModeSession, ReasonMaintenanceDedicated},
		{"gate off", func(in *DecideInput) {
			in.Owner = web
			in.UserModeAvailable = false
		}, ModeSession, ReasonUserModeUnavailable},
		{"tenant api key", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "api_tenant_key:7:3", Kind: KindAPITenantKey, Source: SourceAPITenantKey}
		}, ModeSession, ReasonSharedAPIKey},
		{"platform api key", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "api_platform:3", Kind: KindAPIPlatformKey, Source: SourceAPIPlatformKey}
		}, ModeSession, ReasonSharedAPIKey},
		{"mcp endpoint", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "mcp_endpoint:7:e", Kind: KindMCPEndpoint, Source: SourceMCPEndpoint}
		}, ModeSession, ReasonSharedMCPEndpoint},
		{"embed", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "embed_session:7:c:s", Kind: KindEmbed}
		}, ModeSession, ReasonChannelIdentity},
		{"im", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "x", Kind: KindIM}
		}, ModeSession, ReasonChannelIdentity},
		{"empty owner", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7}
		}, ModeSession, ReasonOwnerIdentityUnverified},
		{"web without web source", func(in *DecideInput) {
			in.Owner = web
			in.Owner.Source = SourceInternal
		}, ModeSession, ReasonOwnerIdentityUnverified},
		{"external with unknown source", func(in *DecideInput) {
			in.Owner = ext
			in.Owner.Source = SourceUnknown
		}, ModeSession, ReasonOwnerIdentityUnverified},
		{"unknown kind", func(in *DecideInput) {
			in.Owner = Owner{TenantID: 7, ID: "u", Source: SourceWeb}
		}, ModeSession, ReasonOwnerIdentityUnverified},
		{"owner with braces", func(in *DecideInput) {
			in.Owner = web
			in.Owner.ID = "u{1}"
		}, ModeSession, ReasonOwnerIdentityInvalid},
		{"owner too long", func(in *DecideInput) {
			in.Owner = web
			in.Owner.ID = strings.Repeat("a", 513)
		}, ModeSession, ReasonOwnerIdentityInvalid},
		{"session id not uuid", func(in *DecideInput) {
			in.Owner = web
			in.SessionID = "abc"
		}, ModeSession, ReasonSessionIDNotUUID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			got := Decide(in)
			require.Equal(t, tc.mode, got.Mode)
			require.Equal(t, tc.reason, got.Reason)
		})
	}
}

func TestEligibleForUser(t *testing.T) {
	require.True(t, EligibleForUser(KindWebUser, SourceWeb))
	require.True(t, EligibleForUser(KindAPIExternalUser, SourceAPISignedToken))
	require.True(t, EligibleForUser(KindAPIExternalUser, SourceAPIDirectHeader))
	require.False(t, EligibleForUser(KindWebUser, SourceInternal))
	require.False(t, EligibleForUser(KindAPITenantKey, SourceAPITenantKey))
	require.False(t, EligibleForUser(KindUnknown, SourceWeb))
}

func TestUserMachineDigestIsStableAndUnambiguous(t *testing.T) {
	m := UserMachine{OwnerTenantID: 7, OwnerID: "u-1", ConfigTenantID: 9, ConfigID: "cfg"}
	require.NoError(t, m.Validate())
	d := m.Digest("weknora")
	require.Len(t, d, 64)
	require.Equal(t, d, m.Digest("weknora"))
	require.NotEqual(t, d, m.Digest("other"))
	// Length-prefixing keeps field boundaries: these two must not collide.
	a := UserMachine{OwnerTenantID: 7, OwnerID: "ab", ConfigTenantID: 9, ConfigID: "c"}
	b := UserMachine{OwnerTenantID: 7, OwnerID: "a", ConfigTenantID: 9, ConfigID: "bc"}
	require.NotEqual(t, a.Digest("n"), b.Digest("n"))

	require.Error(t, UserMachine{OwnerID: "u", ConfigTenantID: 9, ConfigID: "c"}.Validate())
	require.Error(t, UserMachine{OwnerTenantID: 7, ConfigTenantID: 9, ConfigID: "c"}.Validate())
	require.Error(t, UserMachine{OwnerTenantID: 7, OwnerID: "u", ConfigID: "c"}.Validate())
	require.Error(t, UserMachine{OwnerTenantID: 7, OwnerID: "u", ConfigTenantID: 9}.Validate())
}

func TestAssignmentLayout(t *testing.T) {
	s := Assignment{SessionID: sid, ConfigTenantID: 9, ConfigID: "cfg"}
	require.Equal(t, ModeSession, s.Mode())
	l, err := s.Layout()
	require.NoError(t, err)
	require.Equal(t, "/workspace", l.Root)
	_, ok := s.UserMachine()
	require.False(t, ok)

	want := UserMachine{OwnerTenantID: 7, OwnerID: "u-1", ConfigTenantID: 9, ConfigID: "cfg"}
	u := Assignment{SessionID: sid, ConfigTenantID: 9, ConfigID: "cfg", Machine: want}
	require.Equal(t, ModeUser, u.Mode())
	l, err = u.Layout()
	require.NoError(t, err)
	require.Equal(t, "/workspace/sessions/c812e1edb644", l.Root)
	m, ok := u.UserMachine()
	require.True(t, ok)
	require.Equal(t, want, m)
}

func TestRetention(t *testing.T) {
	require.Equal(t, 7, EffectiveRetentionDays(0))
	require.Equal(t, 30, EffectiveRetentionDays(30))
	require.NoError(t, ValidateRetentionDays(0))
	require.NoError(t, ValidateRetentionDays(90))
	require.Error(t, ValidateRetentionDays(-1))
	require.Error(t, ValidateRetentionDays(91))
}

func TestGate(t *testing.T) {
	t.Cleanup(func() { SetUserModeAvailable(false) })
	require.False(t, UserModeAvailable())
	SetUserModeAvailable(true)
	require.True(t, UserModeAvailable())
	t.Setenv(UserModeEnvVar, "enabled")
	require.True(t, UserModeRequestedByEnv())
	t.Setenv(UserModeEnvVar, "nope")
	require.False(t, UserModeRequestedByEnv())
}
