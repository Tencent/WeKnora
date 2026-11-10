package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/storageallowlist"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StorageBackendSourceUser     = "user"
	StorageBackendSourceEnv      = "env"
	StorageBackendStatusActive   = "active"
	StorageBackendStatusDisabled = "disabled"
)

// StorageBackend is one concrete file/object storage instance. A workspace may
// register multiple instances of the same provider and bind each knowledge base
// to a different instance.
type StorageBackend struct {
	ID          string               `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64               `json:"tenant_id" gorm:"not null;index"`
	Name        string               `json:"name" gorm:"type:varchar(255);not null"`
	Provider    string               `json:"provider" gorm:"type:varchar(32);not null;index"`
	Config      StorageBackendConfig `json:"config" gorm:"type:json"`
	Source      string               `json:"source" gorm:"type:varchar(16);not null;default:'user'"`
	Status      string               `json:"status" gorm:"type:varchar(16);not null;default:'active'"`
	LegacyAlias bool                 `json:"legacy_alias" gorm:"not null;default:false"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	DeletedAt   gorm.DeletedAt       `json:"deleted_at" gorm:"index"`
}

func (StorageBackend) TableName() string { return "storage_backends" }

func (b *StorageBackend) BeforeCreate(_ *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Source == "" {
		b.Source = StorageBackendSourceUser
	}
	if b.Status == "" {
		b.Status = StorageBackendStatusActive
	}
	return nil
}

func (b *StorageBackend) Validate() error {
	if b.TenantID == 0 {
		return errors.NewValidationError("tenant_id is required")
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return errors.NewValidationError("name is required")
	}
	b.Provider = strings.ToLower(strings.TrimSpace(b.Provider))
	if !storageallowlist.IsAllowed(b.Provider) {
		return errors.NewValidationError(fmt.Sprintf("storage provider %q is not allowed", b.Provider))
	}
	if !isSupportedStorageBackendProvider(b.Provider) {
		return errors.NewValidationError(fmt.Sprintf("unsupported storage provider: %s", b.Provider))
	}
	if b.Status == "" {
		b.Status = StorageBackendStatusActive
	}
	if b.Status != StorageBackendStatusActive && b.Status != StorageBackendStatusDisabled {
		return errors.NewValidationError("status must be active or disabled")
	}
	return b.Config.ValidateForProvider(b.Provider)
}

func isSupportedStorageBackendProvider(provider string) bool {
	for _, candidate := range storageallowlist.Supported() {
		if provider == candidate {
			return true
		}
	}
	return false
}

// StorageBackendConfig is the normalized union of provider-specific storage
// settings. AccessKeyID/SecretAccessKey map to COS SecretID/SecretKey and to
// the access/secret key pair used by S3-compatible providers.
type StorageBackendConfig struct {
	Mode            string `json:"mode,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	BucketName      string `json:"bucket_name,omitempty"`
	PathPrefix      string `json:"path_prefix,omitempty"`
	AppID           string `json:"app_id,omitempty"`
	UseSSL          bool   `json:"use_ssl,omitempty"`
	ForcePathStyle  bool   `json:"force_path_style,omitempty"`
	UseTempBucket   bool   `json:"use_temp_bucket,omitempty"`
	TempBucketName  string `json:"temp_bucket_name,omitempty"`
	TempRegion      string `json:"temp_region,omitempty"`
}

func (c StorageBackendConfig) Value() (driver.Value, error) {
	if key := utils.GetAESKey(); key != nil {
		if c.AccessKeyID != "" {
			if encrypted, err := utils.EncryptAESGCM(c.AccessKeyID, key); err == nil {
				c.AccessKeyID = encrypted
			}
		}
		if c.SecretAccessKey != "" {
			if encrypted, err := utils.EncryptAESGCM(c.SecretAccessKey, key); err == nil {
				c.SecretAccessKey = encrypted
			}
		}
	}
	return json.Marshal(c)
}

func (c *StorageBackendConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(b, c); err != nil {
		return err
	}
	accessKey, err := utils.DecryptStoredSecret(c.AccessKeyID)
	if err != nil {
		return fmt.Errorf("decrypt storage backend access key: %w", err)
	}
	secretKey, err := utils.DecryptStoredSecret(c.SecretAccessKey)
	if err != nil {
		return fmt.Errorf("decrypt storage backend secret key: %w", err)
	}
	c.AccessKeyID = accessKey
	c.SecretAccessKey = secretKey
	return nil
}

func (c StorageBackendConfig) MaskSensitiveFields() StorageBackendConfig {
	out := c
	if out.AccessKeyID != "" {
		out.AccessKeyID = RedactedSecretPlaceholder
	}
	if out.SecretAccessKey != "" {
		out.SecretAccessKey = RedactedSecretPlaceholder
	}
	return out
}

func (c StorageBackendConfig) MergeSecrets(existing StorageBackendConfig) StorageBackendConfig {
	c.AccessKeyID = PreserveIfRedacted(c.AccessKeyID, existing.AccessKeyID)
	c.SecretAccessKey = PreserveIfRedacted(c.SecretAccessKey, existing.SecretAccessKey)
	return c
}

func (c StorageBackendConfig) ValidateForProvider(provider string) error {
	prefix := strings.ReplaceAll(strings.TrimSpace(c.PathPrefix), "\\", "/")
	cleanPrefix := path.Clean(prefix)
	if strings.HasPrefix(prefix, "/") || cleanPrefix == ".." || strings.HasPrefix(cleanPrefix, "../") {
		return errors.NewValidationError("path_prefix must be a relative path without parent traversal")
	}
	required := func(name, value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.NewValidationError(name + " is required")
		}
		return nil
	}
	switch provider {
	case "local":
		return nil
	case "minio":
		if c.Mode == "" {
			c.Mode = "remote"
		}
		if c.Mode != "docker" {
			for name, value := range map[string]string{"endpoint": c.Endpoint, "access_key_id": c.AccessKeyID, "secret_access_key": c.SecretAccessKey} {
				if err := required(name, value); err != nil {
					return err
				}
			}
		}
		return required("bucket_name", c.BucketName)
	case "cos":
		for name, value := range map[string]string{"region": c.Region, "access_key_id": c.AccessKeyID, "secret_access_key": c.SecretAccessKey, "bucket_name": c.BucketName} {
			if err := required(name, value); err != nil {
				return err
			}
		}
		return nil
	default:
		for name, value := range map[string]string{"endpoint": c.Endpoint, "region": c.Region, "access_key_id": c.AccessKeyID, "secret_access_key": c.SecretAccessKey, "bucket_name": c.BucketName} {
			if err := required(name, value); err != nil {
				return err
			}
		}
		return nil
	}
}

// LocationKey identifies the physical destination. Credentials deliberately do
// not participate so they can be rotated without changing object identity.
func (c StorageBackendConfig) LocationKey(provider string) string {
	mode := strings.TrimSpace(c.Mode)
	if provider == "minio" && mode == "" {
		mode = "remote"
	}
	return strings.Join([]string{provider, mode, strings.TrimSpace(c.Endpoint), strings.TrimSpace(c.Region), strings.TrimSpace(c.BucketName), strings.Trim(strings.TrimSpace(c.PathPrefix), "/")}, "|")
}

// ToStorageEngineConfig adapts the instance model to the existing provider
// implementations while those implementations are progressively normalized.
func (b StorageBackend) ToStorageEngineConfig() *StorageEngineConfig {
	c := b.Config
	result := &StorageEngineConfig{DefaultProvider: b.Provider}
	switch b.Provider {
	case "local":
		result.Local = &LocalEngineConfig{PathPrefix: c.PathPrefix}
	case "minio":
		mode := c.Mode
		if mode == "" {
			mode = "remote"
		}
		result.MinIO = &MinIOEngineConfig{Mode: mode, Endpoint: c.Endpoint, AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, BucketName: c.BucketName, UseSSL: c.UseSSL, PathPrefix: c.PathPrefix}
	case "cos":
		result.COS = &COSEngineConfig{SecretID: c.AccessKeyID, SecretKey: c.SecretAccessKey, Region: c.Region, BucketName: c.BucketName, AppID: c.AppID, PathPrefix: c.PathPrefix, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "tos":
		result.TOS = &TOSEngineConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKey: c.AccessKeyID, SecretKey: c.SecretAccessKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "s3":
		result.S3 = &S3EngineConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKey: c.AccessKeyID, SecretKey: c.SecretAccessKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseSSL: c.UseSSL, ForcePathStyle: c.ForcePathStyle}
	case "oss":
		result.OSS = &OSSEngineConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKey: c.AccessKeyID, SecretKey: c.SecretAccessKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseTempBucket: c.UseTempBucket, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "ks3":
		result.KS3 = &KS3EngineConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKey: c.AccessKeyID, SecretKey: c.SecretAccessKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix}
	case "obs":
		result.OBS = &OBSEngineConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKey: c.AccessKeyID, SecretKey: c.SecretAccessKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseSSL: c.UseSSL}
	}
	return result
}

func NewStorageBackendResponse(backend *StorageBackend) StorageBackend {
	out := *backend
	out.Config = backend.Config.MaskSensitiveFields()
	return out
}

// StorageBackendFromLegacy projects one provider entry from the old workspace
// singleton JSON into the multi-instance model.
func StorageBackendFromLegacy(tenantID uint64, provider string, legacy *StorageEngineConfig) *StorageBackend {
	if legacy == nil {
		return nil
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	b := &StorageBackend{TenantID: tenantID, Provider: provider, Source: StorageBackendSourceUser, Status: StorageBackendStatusActive, LegacyAlias: true}
	switch provider {
	case "local":
		if legacy.Local == nil {
			return nil
		}
		b.Name, b.Config.PathPrefix = "Local", legacy.Local.PathPrefix
	case "minio":
		if legacy.MinIO == nil {
			return nil
		}
		c := legacy.MinIO
		b.Name = "MinIO"
		b.Config = StorageBackendConfig{Mode: c.Mode, Endpoint: c.Endpoint, AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, BucketName: c.BucketName, UseSSL: c.UseSSL, PathPrefix: c.PathPrefix}
	case "cos":
		if legacy.COS == nil {
			return nil
		}
		c := legacy.COS
		b.Name = "COS"
		b.Config = StorageBackendConfig{Region: c.Region, AccessKeyID: c.SecretID, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, AppID: c.AppID, PathPrefix: c.PathPrefix, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "tos":
		if legacy.TOS == nil {
			return nil
		}
		c := legacy.TOS
		b.Name = "TOS"
		b.Config = StorageBackendConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "s3":
		if legacy.S3 == nil {
			return nil
		}
		c := legacy.S3
		b.Name = "S3"
		b.Config = StorageBackendConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseSSL: c.UseSSL, ForcePathStyle: c.ForcePathStyle}
	case "oss":
		if legacy.OSS == nil {
			return nil
		}
		c := legacy.OSS
		b.Name = "OSS"
		b.Config = StorageBackendConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseTempBucket: c.UseTempBucket, TempBucketName: c.TempBucketName, TempRegion: c.TempRegion}
	case "ks3":
		if legacy.KS3 == nil {
			return nil
		}
		c := legacy.KS3
		b.Name = "KS3"
		b.Config = StorageBackendConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix}
	case "obs":
		if legacy.OBS == nil {
			return nil
		}
		c := legacy.OBS
		b.Name = "OBS"
		b.Config = StorageBackendConfig{Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, BucketName: c.BucketName, PathPrefix: c.PathPrefix, UseSSL: c.UseSSL}
	default:
		return nil
	}
	return b
}

// StorageBackendFromEnvironment snapshots the process-wide storage backend for
// a workspace. The row is read-only in the UI and keeps env-only deployments
// on the same instance-resolution path as user-managed backends.
func StorageBackendFromEnvironment(tenantID uint64) *StorageBackend {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
	if provider == "" {
		provider = "local"
	}
	b := &StorageBackend{
		TenantID: tenantID, Name: "System " + strings.ToUpper(provider), Provider: provider,
		Source: StorageBackendSourceEnv, Status: StorageBackendStatusActive, LegacyAlias: true,
	}
	switch provider {
	case "local":
		b.Config.PathPrefix = strings.TrimSpace(os.Getenv("LOCAL_STORAGE_PATH_PREFIX"))
	case "minio":
		b.Config = StorageBackendConfig{Mode: "remote", Endpoint: os.Getenv("MINIO_ENDPOINT"), AccessKeyID: os.Getenv("MINIO_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("MINIO_SECRET_ACCESS_KEY"), BucketName: os.Getenv("MINIO_BUCKET_NAME"), PathPrefix: os.Getenv("MINIO_PATH_PREFIX"), UseSSL: strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true")}
	case "cos":
		b.Config = StorageBackendConfig{Region: os.Getenv("COS_REGION"), AccessKeyID: os.Getenv("COS_SECRET_ID"), SecretAccessKey: os.Getenv("COS_SECRET_KEY"), BucketName: os.Getenv("COS_BUCKET_NAME"), AppID: os.Getenv("COS_APP_ID"), PathPrefix: os.Getenv("COS_PATH_PREFIX"), TempBucketName: os.Getenv("COS_TEMP_BUCKET_NAME"), TempRegion: os.Getenv("COS_TEMP_REGION")}
	case "tos":
		b.Config = StorageBackendConfig{Endpoint: os.Getenv("TOS_ENDPOINT"), Region: os.Getenv("TOS_REGION"), AccessKeyID: os.Getenv("TOS_ACCESS_KEY"), SecretAccessKey: os.Getenv("TOS_SECRET_KEY"), BucketName: os.Getenv("TOS_BUCKET_NAME"), PathPrefix: os.Getenv("TOS_PATH_PREFIX"), TempBucketName: os.Getenv("TOS_TEMP_BUCKET_NAME"), TempRegion: os.Getenv("TOS_TEMP_REGION")}
	case "s3":
		b.Config = StorageBackendConfig{Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"), AccessKeyID: os.Getenv("S3_ACCESS_KEY"), SecretAccessKey: os.Getenv("S3_SECRET_KEY"), BucketName: os.Getenv("S3_BUCKET_NAME"), PathPrefix: os.Getenv("S3_PATH_PREFIX"), UseSSL: !strings.EqualFold(os.Getenv("S3_USE_SSL"), "false"), ForcePathStyle: strings.EqualFold(os.Getenv("S3_FORCE_PATH_STYLE"), "true")}
	case "oss":
		b.Config = StorageBackendConfig{Endpoint: os.Getenv("OSS_ENDPOINT"), Region: os.Getenv("OSS_REGION"), AccessKeyID: os.Getenv("OSS_ACCESS_KEY"), SecretAccessKey: os.Getenv("OSS_SECRET_KEY"), BucketName: os.Getenv("OSS_BUCKET_NAME"), PathPrefix: os.Getenv("OSS_PATH_PREFIX"), UseTempBucket: os.Getenv("OSS_TEMP_BUCKET_NAME") != "", TempBucketName: os.Getenv("OSS_TEMP_BUCKET_NAME"), TempRegion: os.Getenv("OSS_TEMP_REGION")}
	case "obs":
		b.Config = StorageBackendConfig{Endpoint: os.Getenv("OBS_ENDPOINT"), Region: os.Getenv("OBS_REGION"), AccessKeyID: os.Getenv("OBS_ACCESS_KEY"), SecretAccessKey: os.Getenv("OBS_SECRET_KEY"), BucketName: os.Getenv("OBS_BUCKET_NAME"), PathPrefix: os.Getenv("OBS_PATH_PREFIX"), UseSSL: !strings.EqualFold(os.Getenv("OBS_USE_SSL"), "false")}
	default:
		return nil
	}
	return b
}
