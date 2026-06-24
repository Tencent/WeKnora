package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type ldapAuthResult struct {
	Username    string
	Email       string
	DisplayName string
	UserDN      string
}

func (s *userService) findLoginUser(ctx context.Context, identifier string) (*types.User, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("empty login identifier")
	}

	user, err := s.userRepo.GetUserByEmail(ctx, identifier)
	if err == nil || !isUserLookupNotFound(err) {
		return user, err
	}

	if strings.Contains(identifier, "@") {
		localPart := strings.TrimSpace(strings.Split(identifier, "@")[0])
		if localPart == "" {
			return nil, err
		}
		identifier = localPart
	}
	return s.userRepo.GetUserByUsername(ctx, identifier)
}

func (s *userService) loginWithLDAP(ctx context.Context, identifier, password string) (*types.User, error) {
	cfg := s.ldapConfig()
	if cfg == nil || !cfg.Enable {
		return nil, errors.New("LDAP login is disabled")
	}

	username := ldapUsername(identifier)
	if username == "" || strings.TrimSpace(password) == "" {
		return nil, errors.New("LDAP username and password are required")
	}

	result, err := bindLDAP(cfg, username, password)
	if err != nil {
		return nil, err
	}
	return s.findOrCreateLDAPUser(ctx, result)
}

func (s *userService) ldapConfig() *config.LDAPAuthConfig {
	if s == nil || s.config == nil || s.config.LDAPAuth == nil {
		return nil
	}
	return s.config.LDAPAuth
}

func (s *userService) shouldTryLDAPLogin(identifier string) bool {
	cfg := s.ldapConfig()
	if cfg == nil || !cfg.Enable {
		return false
	}
	return ldapUsername(identifier) != ""
}

func (s *userService) isLDAPLoginRequired() bool {
	cfg := s.ldapConfig()
	return cfg != nil && cfg.Enable && strings.EqualFold(strings.TrimSpace(cfg.LoginMode), config.LDAPLoginModeRequired)
}

func ldapUsername(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return ""
	}
	if strings.Contains(identifier, "@") {
		identifier = strings.Split(identifier, "@")[0]
	}
	return strings.TrimSpace(identifier)
}

func bindLDAP(cfg *config.LDAPAuthConfig, username, password string) (*ldapAuthResult, error) {
	host := strings.TrimSpace(cfg.Host)
	domain := strings.TrimSpace(cfg.Domain)
	if host == "" || domain == "" {
		return nil, errors.New("LDAP host/domain is not configured")
	}

	port := cfg.Port
	if port == 0 {
		if cfg.UseSSL {
			port = 636
		} else {
			port = 389
		}
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	scheme := "ldap"
	if cfg.UseSSL {
		scheme = "ldaps"
	}
	addr := fmt.Sprintf("%s://%s:%d", scheme, host, port)
	conn, err := ldap.DialURL(
		addr,
		ldap.DialWithDialer(&net.Dialer{Timeout: timeout}),
		ldap.DialWithTLSConfig(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect LDAP: %w", err)
	}
	defer conn.Close()

	upn := fmt.Sprintf("%s@%s", username, domain)
	if err := conn.Bind(upn, password); err != nil {
		return nil, fmt.Errorf("bind LDAP: %w", err)
	}

	result := &ldapAuthResult{
		Username: username,
		Email:    fmt.Sprintf("%s@%s", username, domain),
	}

	if dn, err := conn.WhoAmI(nil); err == nil && dn != nil {
		result.UserDN = strings.TrimPrefix(dn.AuthzID, "dn:")
		result.UserDN = strings.TrimPrefix(result.UserDN, "DN:")
	}

	baseDN := strings.TrimSpace(cfg.BaseDN)
	if baseDN == "" {
		if discovered, err := ldapDefaultNamingContext(conn); err == nil {
			baseDN = discovered
		}
	}
	if baseDN == "" {
		return result, nil
	}

	searchReq := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1,
		int(timeout/time.Second),
		false,
		fmt.Sprintf("(sAMAccountName=%s)", ldap.EscapeFilter(username)),
		[]string{"displayName", "mail"},
		nil,
	)
	searchResp, err := conn.Search(searchReq)
	if err != nil || len(searchResp.Entries) == 0 {
		return result, nil
	}

	entry := searchResp.Entries[0]
	if displayName := strings.TrimSpace(entry.GetAttributeValue("displayName")); displayName != "" {
		result.DisplayName = displayName
	}
	if mail := strings.TrimSpace(entry.GetAttributeValue("mail")); mail != "" {
		result.Email = mail
	}
	if entry.DN != "" {
		result.UserDN = entry.DN
	}
	return result, nil
}

func ldapDefaultNamingContext(conn *ldap.Conn) (string, error) {
	searchReq := ldap.NewSearchRequest(
		"",
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1,
		5,
		false,
		"(objectClass=*)",
		[]string{"defaultNamingContext"},
		nil,
	)
	searchResp, err := conn.Search(searchReq)
	if err != nil || len(searchResp.Entries) == 0 {
		return "", err
	}
	return strings.TrimSpace(searchResp.Entries[0].GetAttributeValue("defaultNamingContext")), nil
}

func (s *userService) findOrCreateLDAPUser(ctx context.Context, result *ldapAuthResult) (*types.User, error) {
	if result == nil {
		return nil, errors.New("empty LDAP result")
	}

	email := strings.TrimSpace(result.Email)
	username := sanitizeUsernameCandidate(result.Username)
	if username == "" {
		username = sanitizeUsernameCandidate(strings.Split(email, "@")[0])
	}
	if username == "" {
		return nil, errors.New("LDAP username is empty")
	}
	if email == "" {
		email = username
	}

	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil && !isUserLookupNotFound(err) {
		return nil, err
	}
	if isUserLookupNotFound(err) || user == nil {
		user, err = s.userRepo.GetUserByEmail(ctx, email)
		if err != nil && !isUserLookupNotFound(err) {
			return nil, err
		}
	}
	tenant, err := s.ensureLDAPDefaultTenant(ctx)
	if err != nil {
		return nil, err
	}
	role := ldapDefaultRole(s.ldapConfig())

	if user != nil {
		if !user.IsActive {
			return user, nil
		}
		changed := false
		if user.Username == "" {
			user.Username = username
			changed = true
		}
		if strings.TrimSpace(user.Email) == "" || !strings.Contains(user.Email, "@") {
			user.Email = email
			changed = true
		}
		if changed {
			user.UpdatedAt = time.Now()
			if err := s.userRepo.UpdateUser(ctx, user); err != nil {
				return nil, fmt.Errorf("update LDAP user: %w", err)
			}
		}
		if err := s.ensureLDAPMembership(ctx, user.ID, tenant.ID, role); err != nil {
			return nil, err
		}
		return user, nil
	}

	randomPassword, err := generateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("generate LDAP placeholder password: %w", err)
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash LDAP placeholder password: %w", err)
	}

	user = &types.User{
		ID:           uuid.New().String(),
		Username:     username,
		Email:        email,
		PasswordHash: string(hashedPassword),
		TenantID:     tenant.ID,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create LDAP user: %w", err)
	}
	if err := s.ensureLDAPMembership(ctx, user.ID, tenant.ID, role); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "LDAP user provisioned username=%s email=%s tenant=%d role=%s dn=%s",
		secutils.SanitizeForLog(username),
		secutils.SanitizeForLog(email),
		tenant.ID,
		role,
		secutils.SanitizeForLog(result.UserDN),
	)
	return user, nil
}

func (s *userService) ensureLDAPDefaultTenant(ctx context.Context) (*types.Tenant, error) {
	if s.tenantService == nil {
		return nil, errors.New("tenant service is unavailable")
	}
	name := ldapDefaultTenantName(s.ldapConfig())
	tenants, err := s.tenantService.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants for LDAP default tenant: %w", err)
	}
	for _, tenant := range tenants {
		if tenant != nil && strings.TrimSpace(tenant.Name) == name {
			return tenant, nil
		}
	}
	tenant, err := s.tenantService.CreateTenant(ctx, &types.Tenant{
		Name:        name,
		Description: "LDAP default workspace",
		Status:      "active",
	})
	if err != nil {
		return nil, fmt.Errorf("create LDAP default workspace: %w", err)
	}
	return tenant, nil
}

func (s *userService) ensureLDAPMembership(ctx context.Context, userID string, tenantID uint64, role types.TenantRole) error {
	if s.memberService == nil {
		return nil
	}
	if role == types.TenantRoleOwner {
		_, err := s.memberService.EnsureOwner(ctx, userID, tenantID)
		if err != nil {
			return fmt.Errorf("ensure LDAP owner membership: %w", err)
		}
		return nil
	}
	member, err := s.memberService.GetMembership(ctx, userID, tenantID)
	if err != nil {
		return fmt.Errorf("lookup LDAP membership: %w", err)
	}
	if member == nil {
		if _, err := s.memberService.AddMember(ctx, userID, tenantID, role, nil); err != nil && !errors.Is(err, ErrMembershipAlreadyExists) {
			return fmt.Errorf("create LDAP membership: %w", err)
		}
		return nil
	}
	if member.Status == types.TenantMemberStatusActive && !member.Role.HasPermission(role) {
		if err := s.memberService.UpdateRole(ctx, userID, tenantID, role); err != nil {
			return fmt.Errorf("update LDAP membership role: %w", err)
		}
	}
	return nil
}

func ldapDefaultTenantName(cfg *config.LDAPAuthConfig) string {
	if cfg != nil {
		if name := strings.TrimSpace(cfg.DefaultTenantName); name != "" {
			return name
		}
	}
	return "LDAP Users"
}

func ldapDefaultRole(cfg *config.LDAPAuthConfig) types.TenantRole {
	if cfg != nil {
		if role := types.TenantRole(strings.TrimSpace(cfg.DefaultRole)); role.IsValid() {
			return role
		}
	}
	return types.TenantRoleContributor
}
