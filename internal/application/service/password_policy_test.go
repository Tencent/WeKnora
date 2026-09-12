package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubComplexPasswordSettings struct {
	interfaces.SystemSettingService
	enabled bool
}

func (s *stubComplexPasswordSettings) GetBool(context.Context, string, string, bool) bool {
	return s.enabled
}

func TestValidatePasswordPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
		complex  bool
		want     error
	}{
		{name: "simple missing digit", password: "password", want: ErrPasswordPolicy},
		{name: "simple too short", password: "Ab1defg", want: ErrPasswordPolicy},
		{name: "simple too long", password: "Abcdefg1" + "xxxxxxxxxxxxxxxxxxxxxxxxx", want: ErrPasswordPolicy},
		{name: "simple ok", password: "password1"},
		{name: "simple unicode letter is not ASCII", password: "Ä1234567", want: ErrPasswordPolicy},
		{name: "complex missing special", password: "NewSecure9", complex: true, want: ErrComplexPasswordPolicy},
		{name: "complex missing upper", password: "newsecure9!", complex: true, want: ErrComplexPasswordPolicy},
		{name: "complex missing lower", password: "NEWSECURE9!", complex: true, want: ErrComplexPasswordPolicy},
		{name: "complex ok", password: "NewSecure9!", complex: true},
		{
			name:     "complex unicode digit is not ASCII",
			password: "Password١!",
			complex:  true,
			want:     ErrComplexPasswordPolicy,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordPolicy(tc.password, tc.complex)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidatePasswordPolicy(%q, %v) err = %v, want %v", tc.password, tc.complex, err, tc.want)
			}
		})
	}
}

func TestResolveComplexPasswordEnabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if got := ResolveComplexPasswordEnabled(ctx, nil, nil); got {
		t.Fatal("nil cfg and settings should default to false")
	}

	// The previous copy-paste checked cfg.Tenant; Auth-only configs must still work.
	cfgAuthOnly := &config.Config{Auth: &config.AuthConfig{ComplexPasswordEnabled: true}}
	if !ResolveComplexPasswordEnabled(ctx, cfgAuthOnly, nil) {
		t.Fatal("Auth.ComplexPasswordEnabled should be honoured when Tenant is nil")
	}

	cfgTenantOnly := &config.Config{Tenant: &config.TenantConfig{}}
	if got := ResolveComplexPasswordEnabled(ctx, cfgTenantOnly, nil); got {
		t.Fatal("Tenant-only cfg must not panic or enable the switch")
	}

	if !ResolveComplexPasswordEnabled(ctx, cfgAuthOnly, &stubComplexPasswordSettings{enabled: true}) {
		t.Fatal("settings override true")
	}
	if ResolveComplexPasswordEnabled(ctx, cfgAuthOnly, &stubComplexPasswordSettings{enabled: false}) {
		t.Fatal("settings override false should win over cfg")
	}
}

func TestIsPasswordPolicyError(t *testing.T) {
	t.Parallel()
	if !IsPasswordPolicyError(ErrPasswordPolicy) || !IsPasswordPolicyError(ErrComplexPasswordPolicy) {
		t.Fatal("policy sentinels should match")
	}
	if IsPasswordPolicyError(ErrSamePassword) {
		t.Fatal("unrelated sentinel must not match")
	}
}
