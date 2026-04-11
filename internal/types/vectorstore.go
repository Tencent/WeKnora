package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EnvStoreIDPrefix is the prefix for virtual env store IDs.
const EnvStoreIDPrefix = "__env_"

// IsEnvStoreID checks if the given ID is an env store virtual ID.
func IsEnvStoreID(id string) bool {
	return strings.HasPrefix(id, EnvStoreIDPrefix)
}

// EnvLookupFunc is a function type for looking up environment variables.
// In production: os.Getenv, in tests: custom lookup function.
type EnvLookupFunc func(string) string

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
	// Version is the detected server version (e.g., "7.10.1", "16.2", "1.12.6").
	// Auto-populated by TestConnection on successful connectivity check.
	Version string `yaml:"version" json:"version,omitempty"`
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

// ---------------------------------------------------------------------------
// VectorStoreResponse — API response DTO
// ---------------------------------------------------------------------------

// VectorStoreResponse is the API response DTO for vector store.
// Wraps VectorStore with additional metadata (source, readonly).
type VectorStoreResponse struct {
	VectorStore
	Source   string `json:"source"`   // "env" or "user"
	ReadOnly bool   `json:"readonly"` // env stores are read-only
}

// NewVectorStoreResponse creates a response DTO from a VectorStore
// with sensitive fields masked.
func NewVectorStoreResponse(store *VectorStore, source string, readonly bool) VectorStoreResponse {
	masked := *store
	masked.ConnectionConfig = store.ConnectionConfig.MaskSensitiveFields()
	return VectorStoreResponse{
		VectorStore: masked,
		Source:      source,
		ReadOnly:    readonly,
	}
}

// ---------------------------------------------------------------------------
// VectorStore type metadata — for /types endpoint
// ---------------------------------------------------------------------------

// VectorStoreTypeInfo describes a supported engine type and its configuration schema.
type VectorStoreTypeInfo struct {
	Type             string                 `json:"type"`
	DisplayName      string                 `json:"display_name"`
	ConnectionFields []VectorStoreFieldInfo `json:"connection_fields"`
	IndexFields      []VectorStoreFieldInfo `json:"index_fields,omitempty"`
}

// VectorStoreFieldInfo describes a single configuration field.
type VectorStoreFieldInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // "string", "number", "boolean"
	Required    bool   `json:"required"`
	Sensitive   bool   `json:"sensitive,omitempty"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// GetVectorStoreTypes returns metadata for all supported engine types.
func GetVectorStoreTypes() []VectorStoreTypeInfo {
	return []VectorStoreTypeInfo{
		{
			Type:        "elasticsearch",
			DisplayName: "Elasticsearch (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "Elasticsearch URL (e.g., http://localhost:9200)"},
				{Name: "username", Type: "string", Required: false},
				{Name: "password", Type: "string", Required: false, Sensitive: true},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "index_name", Type: "string", Required: false, Default: "xwrag_default"},
				{Name: "number_of_shards", Type: "number", Required: false},
				{Name: "number_of_replicas", Type: "number", Required: false},
			},
		},
		{
			Type:        "postgres",
			DisplayName: "PostgreSQL (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "use_default_connection", Type: "boolean", Required: false, Default: true,
					Description: "Use the application's default database connection"},
				{Name: "addr", Type: "string", Required: false,
					Description: "PostgreSQL connection string (required if use_default_connection is false)"},
				{Name: "username", Type: "string", Required: false},
				{Name: "password", Type: "string", Required: false, Sensitive: true},
			},
		},
		{
			Type:        "qdrant",
			DisplayName: "Qdrant (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "host", Type: "string", Required: true, Description: "Qdrant host"},
				{Name: "port", Type: "number", Required: false, Default: 6334},
				{Name: "api_key", Type: "string", Required: false, Sensitive: true},
				{Name: "use_tls", Type: "boolean", Required: false, Default: false},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_prefix", Type: "string", Required: false, Default: "weknora_embeddings"},
			},
		},
		{
			Type:        "milvus",
			DisplayName: "Milvus (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "Milvus address (e.g., localhost:19530)"},
				{Name: "username", Type: "string", Required: false},
				{Name: "password", Type: "string", Required: false, Sensitive: true},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_name", Type: "string", Required: false, Default: "weknora_embeddings"},
			},
		},
		{
			Type:        "weaviate",
			DisplayName: "Weaviate (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "host", Type: "string", Required: true, Description: "Weaviate host (e.g., weaviate:8080)"},
				{Name: "grpc_address", Type: "string", Required: false, Default: "weaviate:50051"},
				{Name: "scheme", Type: "string", Required: false, Default: "http"},
				{Name: "api_key", Type: "string", Required: false, Sensitive: true},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_prefix", Type: "string", Required: false, Default: "WeKnora"},
			},
		},
		{
			Type:        "sqlite",
			DisplayName: "SQLite (Keywords + Vector)",
			ConnectionFields: []VectorStoreFieldInfo{},
		},
	}
}

// ---------------------------------------------------------------------------
// BuildEnvVectorStores — virtual stores from RETRIEVE_DRIVER env var
// ---------------------------------------------------------------------------

// BuildEnvVectorStores builds virtual VectorStore entries from RETRIEVE_DRIVER.
// Returns []VectorStore (not VectorStoreResponse) so that business logic (e.g.,
// duplicate checking) can use them directly. API responses should wrap them
// via NewVectorStoreResponse.
//
// Pure function — does not call os.Getenv directly.
//
// Usage:
//
//	types.BuildEnvVectorStores(os.Getenv("RETRIEVE_DRIVER"), os.Getenv)
func BuildEnvVectorStores(retrieveDriver string, envLookup EnvLookupFunc) []VectorStore {
	if retrieveDriver == "" {
		return nil
	}

	drivers := strings.Split(retrieveDriver, ",")
	var stores []VectorStore

	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "" {
			continue
		}

		store := buildEnvStoreForDriver(driver, envLookup)
		if store != nil {
			stores = append(stores, *store)
		}
	}
	return stores
}

// FindEnvVectorStore finds a specific env store by its virtual ID.
func FindEnvVectorStore(retrieveDriver string, envLookup EnvLookupFunc, id string) *VectorStore {
	for _, s := range BuildEnvVectorStores(retrieveDriver, envLookup) {
		if s.ID == id {
			return &s
		}
	}
	return nil
}

func buildEnvStoreForDriver(driver string, envLookup EnvLookupFunc) *VectorStore {
	switch driver {
	case "postgres":
		return &VectorStore{
			ID:         "__env_postgres__",
			Name:       "postgres (env)",
			EngineType: PostgresRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				UseDefaultConnection: true,
			},
		}
	case "sqlite":
		return &VectorStore{
			ID:         "__env_sqlite__",
			Name:       "sqlite (env)",
			EngineType: SQLiteRetrieverEngineType,
		}
	case "elasticsearch_v8":
		return &VectorStore{
			ID:         "__env_elasticsearch_v8__",
			Name:       "elasticsearch v8 (env)",
			EngineType: ElasticsearchRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("ELASTICSEARCH_ADDR"),
				Username: envLookup("ELASTICSEARCH_USERNAME"),
				Password: envLookup("ELASTICSEARCH_PASSWORD"),
			},
			IndexConfig: IndexConfig{
				IndexName: envLookup("ELASTICSEARCH_INDEX"),
			},
		}
	case "elasticsearch_v7":
		return &VectorStore{
			ID:         "__env_elasticsearch_v7__",
			Name:       "elasticsearch v7 (env)",
			EngineType: ElasticsearchRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("ELASTICSEARCH_ADDR"),
				Username: envLookup("ELASTICSEARCH_USERNAME"),
				Password: envLookup("ELASTICSEARCH_PASSWORD"),
			},
			IndexConfig: IndexConfig{
				IndexName: envLookup("ELASTICSEARCH_INDEX"),
			},
		}
	case "qdrant":
		return &VectorStore{
			ID:         "__env_qdrant__",
			Name:       "qdrant (env)",
			EngineType: QdrantRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Host:   envLookup("QDRANT_HOST"),
				APIKey: envLookup("QDRANT_API_KEY"),
			},
		}
	case "milvus":
		return &VectorStore{
			ID:         "__env_milvus__",
			Name:       "milvus (env)",
			EngineType: MilvusRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("MILVUS_ADDRESS"),
				Username: envLookup("MILVUS_USERNAME"),
				Password: envLookup("MILVUS_PASSWORD"),
			},
		}
	case "weaviate":
		return &VectorStore{
			ID:         "__env_weaviate__",
			Name:       "weaviate (env)",
			EngineType: WeaviateRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Host:        envLookup("WEAVIATE_HOST"),
				GrpcAddress: envLookup("WEAVIATE_GRPC_ADDRESS"),
				Scheme:      envLookup("WEAVIATE_SCHEME"),
				APIKey:      envLookup("WEAVIATE_API_KEY"),
			},
		}
	default:
		return nil
	}
}
