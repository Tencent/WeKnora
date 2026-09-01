// Bootstrap-time hooks that run after the DI container is built but
// before the HTTP server starts listening. These are deliberately
// best-effort: any failure here only warns and does NOT abort startup.
// The reasoning is that an operator running with a misconfigured env
// var should still be able to bring the server up (and fix the issue
// from the running instance) rather than have a typo brick the deploy.
package main

import (
	"context"
	"os"
	"strings"

	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// bootstrapEnvVar is the env var that names the email of the user who
// may be granted both platform privileges when the deployment has no
// existing dual-privilege manager.
//
// Why an env var (vs a CLI subcommand)?
//   - Zero-friction in docker-compose / k8s deploys: set it once in the
//     manifest and the very first user account that signs up with that
//     email is auto-promoted, with no extra ops step.
//   - Idempotent: once at least one user holds both system-admin and
//     cross-tenant privileges, bootstrapping is a no-op.
//   - Recovery-oriented: when no dual-privilege manager exists, the named
//     user receives both privileges. This lets fresh and upgraded deployments
//     establish the first manager without editing the database directly.
const bootstrapEnvVar = "WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL"

// runStartupBootstrap consults the env and applies any one-shot
// bootstrap actions. Currently it only handles platform-manager recovery;
// future bootstrap steps (default model seeding, etc.) can be added
// here as additional dig.Invoke calls.
func runStartupBootstrap(c *dig.Container) {
	ctx := context.Background()

	// Legacy hash repair for migration 000065 placeholder rows. Invoked each
	// startup but short-circuits with a cheap EXISTS once every row is
	// backfilled (no api_key decryption on the steady-state path).
	if err := c.Invoke(func(apiKeySvc interfaces.TenantAPIKeyService) {
		if n, err := apiKeySvc.BackfillMissingKeyHashes(ctx); err != nil {
			logger.Warnf(ctx, "[bootstrap] tenant api key hash backfill failed: %v", err)
		} else if n > 0 {
			logger.Infof(ctx, "[bootstrap] backfilled %d legacy tenant api key hash(es)", n)
		}
	}); err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to resolve TenantAPIKeyService: %v", err)
	}

	email := strings.TrimSpace(os.Getenv(bootstrapEnvVar))
	if email == "" {
		return
	}
	// dig.Invoke resolves UserService from the container; if user
	// service registration is broken we want to know loudly, but still
	// not abort startup — bootstrap is best-effort.
	if err := c.Invoke(func(userSvc interfaces.UserService) {
		bootstrapSystemAdmin(ctx, userSvc, email)
	}); err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to resolve UserService: %v", err)
	}
}

// bootstrapSystemAdmin grants both platform privileges to the user identified
// by `email` only when the deployment currently has no dual-privilege manager.
// The function is idempotent and non-fatal — it warns and returns on
// every error path.
//
// The bootstrap intentionally does NOT create a user when the email is
// not yet registered: account creation is a workflow with side effects
// (password hashing, tenant assignment, audit) that we don't want to
// short-circuit. Operators should sign up normally first, then set the
// env var on the next restart.
func bootstrapSystemAdmin(ctx context.Context, userSvc interfaces.UserService, email string) {
	managerCount, err := userSvc.CountCrossTenantAccessManagers(ctx)
	if err != nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: cannot verify existing cross-tenant access managers, skipping bootstrap: %v",
			bootstrapEnvVar, email, err)
		return
	}
	if managerCount > 0 {
		logger.Infof(ctx,
			"[bootstrap] %s=%s: %d cross-tenant access manager(s) already exist (no-op)",
			bootstrapEnvVar, email, managerCount)
		return
	}

	user, err := userSvc.GetUserByEmail(ctx, email)
	if err != nil {
		// "not found" surfaces as an error in this codebase; treat it
		// gently — operators commonly set the var before the user has
		// signed up. The next restart after registration will succeed.
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: user lookup failed (have they signed up yet?): %v",
			bootstrapEnvVar, email, err)
		return
	}
	if user == nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: no matching user (will retry on next restart)",
			bootstrapEnvVar, email)
		return
	}
	if !user.IsActive {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: user %s is disabled; select an active account for recovery",
			bootstrapEnvVar, email, user.ID)
		return
	}
	if !user.IsSystemAdmin {
		systemAdminCount, err := userSvc.CountActiveSystemAdmins(ctx)
		if err != nil {
			logger.Warnf(ctx,
				"[bootstrap] %s=%s: cannot verify existing system admins, skipping bootstrap: %v",
				bootstrapEnvVar, email, err)
			return
		}
		if systemAdminCount > 0 {
			logger.Warnf(ctx,
				"[bootstrap] %s=%s: user %s is not a system admin; select an existing system admin for recovery",
				bootstrapEnvVar, email, user.ID)
			return
		}
		_, _, err = userSvc.GrantSystemAdmin(ctx, user.ID)
		if err != nil {
			logger.Warnf(ctx,
				"[bootstrap] %s=%s: failed to promote user %s: %v",
				bootstrapEnvVar, email, user.ID, err)
			return
		}
	}
	_, _, err = userSvc.GrantCrossTenantAccess(ctx, user.ID)
	if err != nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: failed to grant cross-tenant access to user %s: %v",
			bootstrapEnvVar, email, user.ID, err)
		return
	}
	logger.Infof(ctx,
		"[bootstrap] granted system-admin and cross-tenant access to user %s (%s) via %s",
		user.ID, email, bootstrapEnvVar)
}
