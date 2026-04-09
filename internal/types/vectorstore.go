package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// VectorStore represents a configured vector database instance for a tenant.
// Each tenant can register multiple VectorStore entries (even of the same engine type)
// to support multi-store scenarios (e.g., ES-hot + ES-warm clusters).
type VectorStore struct {
	// Unique identifier (UUID, auto-generated)
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID for scoping
	TenantID uint64 `yaml:"tenant_id" json:"tenant_id"`
	// User-friendly name, e.g., "elasticsearch-hot"
	Name string `yaml:"name" json:"name" gorm:"type:varchar(255);not null"`
	// Engine type: postgres, elasticsearch, qdrant, milvus, weaviate, sqlite
	EngineType RetrieverEngineType `yaml:"engine_type" json:"engine_type" gorm:"type:varchar(50);not null"`
	// Driver-specific connection parameters (sensitive fields encrypted with AES-GCM)
	ConnectionConfig ConnectionConfig `yaml:"connection_config" json:"connection_config" gorm:"type:json"`
	// Optional index/collection configuration (engine-specific defaults if empty)
	IndexConfig IndexConfig `yaml:"index_config" json:"index_config" gorm:"type:json"`
	// Timestamps
	CreatedAt time.Time      `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time      `yaml:"updated_at" json:"updated_at"`
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at" gorm:"index"`
}

// TableName returns the table name for VectorStore
func (VectorStore) TableName() string {
	return "vector_stores"
}

// BeforeCreate is a GORM hook that runs before creating a new record.
// Automatically generates a UUID for new vector stores.
func (v *VectorStore) BeforeCreate(tx *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	return nil
}

// validEngineTypes defines the engine types that can be registered as VectorStore.
// InfinityRetrieverEngineType and ElasticFaissRetrieverEngineType are legacy/experimental
// types that do not have standalone deployable instances, so they are excluded.
var validEngineTypes = map[RetrieverEngineType]bool{
	PostgresRetrieverEngineType:      true,
	ElasticsearchRetrieverEngineType: true,
	QdrantRetrieverEngineType:        true,
	MilvusRetrieverEngineType:        true,
	WeaviateRetrieverEngineType:      true,
	SQLiteRetrieverEngineType:        true,
}

// IsValidEngineType checks whether the given engine type is valid for VectorStore.
func IsValidEngineType(t RetrieverEngineType) bool {
	return validEngineTypes[t]
}

// Validate checks required fields and engine type validity.
func (v *VectorStore) Validate() error {
	if v.Name == "" {
		return errors.NewValidationError("name is required")
	}
	if !validEngineTypes[v.EngineType] {
		return errors.NewValidationError(fmt.Sprintf("unsupported engine type: %s", v.EngineType))
	}
	if v.TenantID == 0 {
		return errors.NewValidationError("tenant_id is required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// ConnectionConfig
// ---------------------------------------------------------------------------

// ConnectionConfig holds driver-specific connection parameters.
// Sensitive fields (Password, APIKey) are encrypted with AES-GCM at rest.
type ConnectionConfig struct {
	// Common
	Addr     string `yaml:"addr" json:"addr,omitempty"`
	Username string `yaml:"username" json:"username,omitempty"`
	Password string `yaml:"password" json:"password,omitempty"` // AES-GCM encrypted
	APIKey   string `yaml:"api_key" json:"api_key,omitempty"`   // AES-GCM encrypted
	// Qdrant
	Host   string `yaml:"host" json:"host,omitempty"`
	Port   int    `yaml:"port" json:"port,omitempty"`
	UseTLS bool   `yaml:"use_tls" json:"use_tls,omitempty"`
	// Weaviate
	GrpcAddress string `yaml:"grpc_address" json:"grpc_address,omitempty"`
	Scheme      string `yaml:"scheme" json:"scheme,omitempty"`
	// Postgres
	UseDefaultConnection bool `yaml:"use_default_connection" json:"use_default_connection,omitempty"`
}

// Value implements the driver.Valuer interface.
// Encrypts Password and APIKey before persisting to database.
func (c ConnectionConfig) Value() (driver.Value, error) {
	if key := utils.GetAESKey(); key != nil {
		if c.Password != "" {
			if encrypted, err := utils.EncryptAESGCM(c.Password, key); err == nil {
				c.Password = encrypted
			}
		}
		if c.APIKey != "" {
			if encrypted, err := utils.EncryptAESGCM(c.APIKey, key); err == nil {
				c.APIKey = encrypted
			}
		}
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface.
// Decrypts Password and APIKey after loading from database.
func (c *ConnectionConfig) Scan(value interface{}) error {
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
	if key := utils.GetAESKey(); key != nil {
		if c.Password != "" {
			if decrypted, err := utils.DecryptAESGCM(c.Password, key); err == nil {
				c.Password = decrypted
			}
		}
		if c.APIKey != "" {
			if decrypted, err := utils.DecryptAESGCM(c.APIKey, key); err == nil {
				c.APIKey = decrypted
			}
		}
	}
	return nil
}

// GetEndpoint returns a normalized endpoint string for duplicate detection.
func (c ConnectionConfig) GetEndpoint() string {
	if c.Addr != "" {
		return c.Addr
	}
	if c.Host != "" {
		port := c.Port
		if port == 0 {
			port = 6334 // Qdrant default port
		}
		return fmt.Sprintf("%s:%d", c.Host, port)
	}
	if c.UseDefaultConnection {
		return "__default_postgres__"
	}
	return ""
}

// MaskSensitiveFields returns a copy with Password and APIKey masked.
func (c ConnectionConfig) MaskSensitiveFields() ConnectionConfig {
	masked := c
	if masked.Password != "" {
		masked.Password = "***"
	}
	if masked.APIKey != "" {
		masked.APIKey = "***"
	}
	return masked
}

// ---------------------------------------------------------------------------
// IndexConfig
// ---------------------------------------------------------------------------

// IndexConfig holds optional index/collection configuration for the vector store.
// If empty, engine-specific defaults are used.
type IndexConfig struct {
	IndexName        string `yaml:"index_name" json:"index_name,omitempty"`                 // ES, OpenSearch
	NumberOfShards   int    `yaml:"number_of_shards" json:"number_of_shards,omitempty"`     // ES, OpenSearch
	NumberOfReplicas int    `yaml:"number_of_replicas" json:"number_of_replicas,omitempty"` // ES, OpenSearch
	CollectionPrefix string `yaml:"collection_prefix" json:"collection_prefix,omitempty"`   // Qdrant
	CollectionName   string `yaml:"collection_name" json:"collection_name,omitempty"`       // Milvus
}

// Value implements the driver.Valuer interface.
func (c IndexConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface.
func (c *IndexConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// GetIndexNameOrDefault returns the effective index/collection name,
// falling back to engine-specific defaults when the user has not specified one.
func (c IndexConfig) GetIndexNameOrDefault(engineType RetrieverEngineType) string {
	switch engineType {
	case ElasticsearchRetrieverEngineType:
		if c.IndexName != "" {
			return c.IndexName
		}
		return "xwrag_default"
	case QdrantRetrieverEngineType:
		if c.CollectionPrefix != "" {
			return c.CollectionPrefix
		}
		return "weknora_embeddings"
	case MilvusRetrieverEngineType:
		if c.CollectionName != "" {
			return c.CollectionName
		}
		return "weknora_embeddings"
	case WeaviateRetrieverEngineType:
		if c.CollectionPrefix != "" {
			return c.CollectionPrefix
		}
		return "WeKnora"
	default:
		return c.IndexName
	}
}
