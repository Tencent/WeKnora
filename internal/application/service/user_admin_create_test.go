package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/crypto/bcrypt"
)

// adminCreateUserRepo records the Register call so tests can verify both
// the persisted user and the exact password bytes handed to bcrypt.
type adminCreateUserRepo struct {
	interfaces.UserRepository
	existingByEmail    *types.User
	existingByUsername *types.User
	created            *types.User
}

func (r *adminCreateUserRepo) GetUserByEmail(context.Context, string) (*types.User, error) {
	if r.existingByEmail != nil {
		return r.existingByEmail, nil
	}
	return nil, nil
}

func (r *adminCreateUserRepo) GetUserByUsername(context.Context, string) (*types.User, error) {
	if r.existingByUsername != nil {
		return r.existingByUsername, nil
	}
	return nil, nil
}

func (r *adminCreateUserRepo) CreateUser(_ context.Context, user *types.User) error {
	copied := *user
	r.created = &copied
	return nil
}

func newAdminCreateUserService(repo *adminCreateUserRepo) *userService {
	return &userService{userRepo: repo, tenantService: nil, memberService: nil}
}

func TestAdminCreateUserGeneratesPolicyCompliantPasswordWhenEmpty(t *testing.T) {
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	user, generated, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com",
	}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}
	if generated == "" {
		t.Fatal("expected a generated password")
	}
	if user == nil || repo.created == nil {
		t.Fatalf("user was not persisted: %v", repo.created)
	}
	if err := ValidatePasswordPolicy(generated); err != nil {
		t.Fatalf("generated password violates the policy: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.created.PasswordHash), []byte(generated)) != nil {
		t.Fatal("persisted hash does not match the generated password")
	}
}

func TestAdminCreateUserUsesExplicitPassword(t *testing.T) {
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	user, generated, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: new("PlainPass9"),
	}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}
	if generated != "" {
		t.Fatalf("generated password must be empty for a caller-supplied password, got %q", generated)
	}
	if user == nil {
		t.Fatal("user is nil")
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.created.PasswordHash), []byte("PlainPass9")) != nil {
		t.Fatal("persisted hash does not match the explicit password")
	}
}

func TestAdminCreateUserHashesUntrimmedPasswordByteForByte(t *testing.T) {
	// Leading/trailing whitespace is part of the credential.
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	raw := "  PlainPass9  "
	if _, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: &raw,
	}, types.TenantProvisioningTenantless); err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.created.PasswordHash), []byte(raw)) != nil {
		t.Fatal("hash does not match the raw password bytes")
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.created.PasswordHash), []byte(strings.TrimSpace(raw))) == nil {
		t.Fatal("hash matches the trimmed password, the credential was rewritten")
	}
}

func TestAdminCreateUserRejectsPolicyViolatingPassword(t *testing.T) {
	// Registration accepts whitespace as literal password characters, but
	// admin-create policy-checks any provided value.
	// Only an absent password triggers generation.
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	for _, pw := range []string{"password", "", "   ", "\t\n", " \u00a0\u00a0 "} {
		_, generated, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
			Username: "alice", Email: "alice@example.com", Password: &pw,
		}, types.TenantProvisioningTenantless)
		if !errors.Is(err, ErrPasswordPolicy) {
			t.Fatalf("password=%q err=%v, want ErrPasswordPolicy", pw, err)
		}
		if generated != "" {
			t.Fatalf("password=%q generated=%q, want no generated password", pw, generated)
		}
		if repo.created != nil {
			t.Fatalf("password=%q reached persistence", pw)
		}
	}
}

func TestGeneratePolicyCompliantPasswordAlwaysComplies(t *testing.T) {
	// ~0.4% of base64url draws have no digit. Regenerating until the
	// policy passes makes compliance certain for every draw.
	for i := range 2000 {
		pw, err := generatePolicyCompliantPassword()
		if err != nil {
			t.Fatalf("iteration %d: generate: %v", i, err)
		}
		if err := ValidatePasswordPolicy(pw); err != nil {
			t.Fatalf("iteration %d: generated password %q violates the policy: %v", i, pw, err)
		}
	}
}

func TestAdminCreateUserRejectsWeakPasswordBeforePersisting(t *testing.T) {
	// Providing the password key with any value subjects it to the
	// policy; the explicit empty string is rejected like any other
	// policy-violating value and never reaches persistence.
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	for _, pw := range []string{"password", ""} {
		_, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
			Username: "alice", Email: "alice@example.com", Password: &pw,
		}, types.TenantProvisioningTenantless)
		if !errors.Is(err, ErrPasswordPolicy) {
			t.Fatalf("password=%q err=%v, want ErrPasswordPolicy", pw, err)
		}
		if repo.created != nil {
			t.Fatalf("password=%q reached persistence", pw)
		}
	}
}

func TestAdminCreateUserDuplicateReturnsExistingUserWithSentinel(t *testing.T) {
	existing := &types.User{ID: "existing", Email: "alice@example.com"}
	repo := &adminCreateUserRepo{existingByEmail: existing}
	svc := newAdminCreateUserService(repo)

	user, generated, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: new("PlainPass9"),
	}, types.TenantProvisioningTenantless)
	if !errors.Is(err, ErrUserEmailExists) {
		t.Fatalf("err=%v, want ErrUserEmailExists", err)
	}
	if user == nil || user.ID != existing.ID {
		t.Fatalf("user=%v, want the existing user %q", user, existing.ID)
	}
	if generated != "" {
		t.Fatalf("generated=%q, want no generated password for an existing user", generated)
	}
	if repo.created != nil {
		t.Fatal("existing user was overwritten")
	}
}

func TestAdminCreateUserDuplicateUsernameReturnsExistingUserWithSentinel(t *testing.T) {
	existing := &types.User{ID: "existing", Username: "alice"}
	repo := &adminCreateUserRepo{existingByUsername: existing}
	svc := newAdminCreateUserService(repo)

	user, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: new("PlainPass9"),
	}, types.TenantProvisioningTenantless)
	if !errors.Is(err, ErrUserUsernameExists) {
		t.Fatalf("err=%v, want ErrUserUsernameExists", err)
	}
	if user == nil || user.ID != existing.ID {
		t.Fatalf("user=%v, want the existing user %q", user, existing.ID)
	}
}

func TestAdminCreateUserDuplicateLookupTargetsSentinelIdentity(t *testing.T) {
	// Register reports a username collision (email free at check time).
	// A user owning the request email appears before the duplicate lookup,
	// as if created concurrently. The lookup must return the user named by
	// the sentinel, not the email owner a fallback would have picked.
	repo := &racyIdentityRepo{
		byUsername: &types.User{ID: "username-owner", Username: "alice"},
		byEmail:    &types.User{ID: "email-owner", Email: "alice@example.com"},
	}
	svc := &userService{userRepo: repo}

	user, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: new("PlainPass9"),
	}, types.TenantProvisioningTenantless)
	if !errors.Is(err, ErrUserUsernameExists) {
		t.Fatalf("err=%v, want ErrUserUsernameExists", err)
	}
	if user == nil || user.ID != repo.byUsername.ID {
		t.Fatalf("user=%v, want the username-collision owner %q", user, repo.byUsername.ID)
	}
	if repo.emailLookups != 1 {
		t.Fatalf("emailLookups=%d, want 1 (Register's check only, the duplicate lookup must not query email)", repo.emailLookups)
	}
}

// racyIdentityRepo simulates a concurrent create between Register's
// duplicate check and AdminCreateUser's duplicate-path lookup: the email
// becomes occupied only on the second query.
type racyIdentityRepo struct {
	interfaces.UserRepository
	byUsername   *types.User
	byEmail      *types.User
	emailLookups int
}

func (r *racyIdentityRepo) GetUserByEmail(_ context.Context, _ string) (*types.User, error) {
	r.emailLookups++
	if r.emailLookups > 1 {
		return r.byEmail, nil
	}
	return nil, nil
}

func (r *racyIdentityRepo) GetUserByUsername(_ context.Context, _ string) (*types.User, error) {
	return r.byUsername, nil
}

func TestAdminCreateUserRejectsMissingIdentity(t *testing.T) {
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	_, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "", Email: "alice@example.com",
	}, types.TenantProvisioningTenantless)
	if err == nil {
		t.Fatal("expected an error for an empty username")
	}
	if repo.created != nil {
		t.Fatal("invalid request reached persistence")
	}
}
