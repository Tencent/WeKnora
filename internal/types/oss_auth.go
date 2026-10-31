package types

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
)

// OSS authentication modes; the empty mode retains legacy static credentials.
const (
	OSSAuthAccessKey  = "access_key"
	OSSAuthECSRAMRole = "ecs_ram_role"
)

var ossRoleNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// ErrOSSRoleRequiresEnvironment also prevents runtime fallback to the server's
// storage identity when a tenant supplies a forbidden role configuration.
var ErrOSSRoleRequiresEnvironment = errors.NewValidationError(
	"OSS ECS RAM role authentication is only available for environment-configured storage",
)

// ValidateTenantOSSAuth rejects server identity selection in legacy tenant configs.
func ValidateTenantOSSAuth(config *StorageEngineConfig) error {
	if config != nil && config.OSS != nil && config.OSS.AuthType == OSSAuthECSRAMRole {
		return ErrOSSRoleRequiresEnvironment
	}
	return nil
}

// ValidateAuthSource enforces the identity boundary independently of connectivity.
func (b StorageBackend) ValidateAuthSource() error {
	if strings.EqualFold(strings.TrimSpace(b.Provider), "oss") &&
		b.Config.AuthType == OSSAuthECSRAMRole && b.Source != StorageBackendSourceEnv {
		return ErrOSSRoleRequiresEnvironment
	}
	return nil
}

// ValidateAuth keeps the legacy empty auth_type equivalent to access_key.
// Role credentials are fetched only when explicitly requested, never as a
// fallback for a missing or invalid static key pair.
func (c OSSEngineConfig) ValidateAuth() error {
	switch c.AuthType {
	case "", OSSAuthAccessKey:
		if strings.TrimSpace(c.AccessKey) == "" || strings.TrimSpace(c.SecretKey) == "" {
			return errors.NewValidationError("OSS Access Key and Secret Key are required for access_key authentication")
		}
		if c.RoleName != "" {
			return errors.NewValidationError("OSS role_name requires ecs_ram_role authentication")
		}
	case OSSAuthECSRAMRole:
		if c.AccessKey != "" || c.SecretKey != "" {
			return errors.NewValidationError(
				"OSS Access Key and Secret Key must be empty for ECS RAM role authentication",
			)
		}
		if c.RoleName != "" &&
			(!ossRoleNamePattern.MatchString(c.RoleName) || c.RoleName == "." || c.RoleName == "..") {
			return errors.NewValidationError("OSS role_name must be 1-64 letters, digits, dots, underscores or hyphens")
		}
	default:
		return errors.NewValidationError("OSS auth_type must be access_key or ecs_ram_role")
	}
	return nil
}

// Validate checks the connection fields and the selected authentication mode.
func (c OSSEngineConfig) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" || strings.TrimSpace(c.Region) == "" ||
		strings.TrimSpace(c.BucketName) == "" {
		return errors.NewValidationError("OSS Endpoint, Region and Bucket Name are required")
	}
	return c.ValidateAuth()
}
