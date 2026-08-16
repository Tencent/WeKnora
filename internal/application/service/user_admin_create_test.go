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
		Username: "alice", Email: "alice@example.com", Password: "PlainPass9",
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
		Username: "alice", Email: "alice@example.com", Password: raw,
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

func TestAdminCreateUserRejectsWhitespaceOnlyPassword(t *testing.T) {
	// Registration accepts whitespace as literal password characters, but
	// a password made up entirely of whitespace carries no letter or
	// digit, so the admin-create policy (aligned with AdminResetPassword)
	// rejects it with ErrPasswordPolicy. Only a truly empty string
	// triggers random generation.
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	for _, pw := range []string{"   ", "\t\n", " \u00a0\u00a0 "} {
		_, generated, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
			Username: "alice", Email: "alice@example.com", Password: pw,
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

func TestAdminCreateUserRejectsWeakPasswordBeforePersisting(t *testing.T) {
	repo := &adminCreateUserRepo{}
	svc := newAdminCreateUserService(repo)

	_, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: "password",
	}, types.TenantProvisioningTenantless)
	if !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("err=%v, want ErrPasswordPolicy", err)
	}
	if repo.created != nil {
		t.Fatal("weak password reached persistence")
	}
}

func TestAdminCreateUserPropagatesDuplicateEmail(t *testing.T) {
	repo := &adminCreateUserRepo{existingByEmail: &types.User{ID: "existing"}}
	svc := newAdminCreateUserService(repo)

	_, _, err := svc.AdminCreateUser(context.Background(), &types.AdminCreateUserRequest{
		Username: "alice", Email: "alice@example.com", Password: "PlainPass9",
	}, types.TenantProvisioningTenantless)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err=%v, want duplicate error", err)
	}
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
