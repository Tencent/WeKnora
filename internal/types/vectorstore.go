package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	PostgresRetrieverEngineType:        true,
	ElasticsearchRetrieverEngineType:   true,
	QdrantRetrieverEngineType:          true,
	MilvusRetrieverEngineType:          true,
	WeaviateRetrieverEngineType:        true,
	DorisRetrieverEngineType:           true,
	SQLiteRetrieverEngineType:          true,
	TencentVectorDBRetrieverEngineType: true,
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
	// Tencent VectorDB and Doris database name.
	Database string `yaml:"database" json:"database,omitempty"`
	// Postgres
	UseDefaultConnection bool `yaml:"use_default_connection" json:"use_default_connection,omitempty"`
	// Doris: HTTP port for Stream Load API (FE default 8030).
	// Addr is reused for the MySQL protocol "host:9030"; HTTPPort + the host of Addr
	// together form the FE HTTP endpoint used by Stream Load.
	HTTPPort int `yaml:"http_port" json:"http_port,omitempty"`
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
	password, err := utils.DecryptStoredSecret(c.Password)
	if err != nil {
		return fmt.Errorf("decrypt vector store connection password: %w", err)
	}
	c.Password = password
	apiKey, err := utils.DecryptStoredSecret(c.APIKey)
	if err != nil {
		return fmt.Errorf("decrypt vector store connection api_key: %w", err)
	}
	c.APIKey = apiKey
	return nil
}

// GetEndpoint returns a normalized endpoint string for duplicate detection.
func (c ConnectionConfig) GetEndpoint() string {
	if c.Addr != "" {
		if c.Database != "" {
			return c.Addr + "/" + c.Database
		}
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
	// --- Existing fields ---
	IndexName        string `yaml:"index_name" json:"index_name,omitempty"`                 // ES, OpenSearch
	NumberOfShards   int    `yaml:"number_of_shards" json:"number_of_shards,omitempty"`     // ES, OpenSearch
	NumberOfReplicas int    `yaml:"number_of_replicas" json:"number_of_replicas,omitempty"` // ES, OpenSearch
	CollectionPrefix string `yaml:"collection_prefix" json:"collection_prefix,omitempty"`   // Qdrant, Weaviate
	CollectionName   string `yaml:"collection_name" json:"collection_name,omitempty"`       // Milvus

	// --- Scalability fields ---
	ShardNumber       int `yaml:"shard_number" json:"shard_number,omitempty"`               // Qdrant: number of shards per collection
	ReplicationFactor int `yaml:"replication_factor" json:"replication_factor,omitempty"`   // Qdrant, Weaviate: number of replicas
	ShardsNum         int `yaml:"shards_num" json:"shards_num,omitempty"`                   // Milvus: number of shards per collection (CreateCollection)
	ReplicaNumber     int `yaml:"replica_number" json:"replica_number,omitempty"`           // Milvus: in-memory replica count (LoadCollection)
	DesiredShardCount int `yaml:"desired_shard_count" json:"desired_shard_count,omitempty"` // Weaviate: number of shards per collection
	BucketsNum        int `yaml:"buckets_num" json:"buckets_num,omitempty"`                 // Doris: number of buckets per table (DISTRIBUTED BY HASH ... BUCKETS N)
	ReplicationNum    int `yaml:"replication_num" json:"replication_num,omitempty"`         // Doris: replication_num PROPERTIES
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
	case TencentVectorDBRetrieverEngineType:
		if c.CollectionName != "" {
			return c.CollectionName
		}
		return "weknora_embeddings"
	case WeaviateRetrieverEngineType:
		if c.CollectionPrefix != "" {
			return c.CollectionPrefix
		}
		return "Weknora_embeddings"
	case DorisRetrieverEngineType:
		// Doris uses the prefix as the table base name; per-dimension tables are
		// suffixed with _<dim> at runtime by the repository layer.
		if c.CollectionPrefix != "" {
			return c.CollectionPrefix
		}
		if c.CollectionName != "" {
			return c.CollectionName
		}
		return "weknora_embeddings"
	default:
		return c.IndexName
	}
}

// ---------------------------------------------------------------------------
// IndexConfig — getter helpers (pointer receiver for nil safety)
// ---------------------------------------------------------------------------

// GetNumberOfShards returns the configured number_of_shards, or def if unset/zero.
func (c *IndexConfig) GetNumberOfShards(def int) int {
	if c != nil && c.NumberOfShards > 0 {
		return c.NumberOfShards
	}
	return def
}

// GetNumberOfReplicas returns the configured number_of_replicas, or def if unset/zero.
// Note: 0 replicas cannot be distinguished from "not set" because the int field with
// json:"omitempty" omits zero values. If zero-replica support is needed in the future,
// change the field type to *int. Currently 0 is treated as "use server default".
func (c *IndexConfig) GetNumberOfReplicas(def int) int {
	if c != nil && c.NumberOfReplicas > 0 {
		return c.NumberOfReplicas
	}
	return def
}

// GetShardNumber returns the configured shard_number (Qdrant), or def if unset/zero.
func (c *IndexConfig) GetShardNumber(def int) int {
	if c != nil && c.ShardNumber > 0 {
		return c.ShardNumber
	}
	return def
}

// GetReplicationFactor returns the configured replication_factor (Qdrant, Weaviate), or def if unset/zero.
func (c *IndexConfig) GetReplicationFactor(def int) int {
	if c != nil && c.ReplicationFactor > 0 {
		return c.ReplicationFactor
	}
	return def
}

// GetShardsNum returns the configured shards_num (Milvus), or def if unset/zero.
func (c *IndexConfig) GetShardsNum(def int) int {
	if c != nil && c.ShardsNum > 0 {
		return c.ShardsNum
	}
	return def
}

// GetReplicaNumber returns the configured replica_number (Milvus in-memory replicas), or def if unset/zero.
// Milvus replicas are set at LoadCollection time, not CreateCollection.
// They control how many query nodes hold the data in memory for read HA/throughput.
func (c *IndexConfig) GetReplicaNumber(def int) int {
	if c != nil && c.ReplicaNumber > 0 {
		return c.ReplicaNumber
	}
	return def
}

// GetDesiredShardCount returns the configured desired_shard_count (Weaviate), or def if unset/zero.
func (c *IndexConfig) GetDesiredShardCount(def int) int {
	if c != nil && c.DesiredShardCount > 0 {
		return c.DesiredShardCount
	}
	return def
}

// GetBucketsNum returns the configured buckets_num (Doris), or def if unset/zero.
func (c *IndexConfig) GetBucketsNum(def int) int {
	if c != nil && c.BucketsNum > 0 {
		return c.BucketsNum
	}
	return def
}

// GetReplicationNum returns the configured replication_num (Doris), or def if unset/zero.
func (c *IndexConfig) GetReplicationNum(def int) int {
	if c != nil && c.ReplicationNum > 0 {
		return c.ReplicationNum
	}
	return def
}

// ---------------------------------------------------------------------------
// IndexConfig — resolve helpers (for Repository layer, with env var fallback)
// ---------------------------------------------------------------------------

// ResolveIndexName returns the index name from IndexConfig, falling back to env var and then default.
// Used by Repository constructors. For service-layer duplicate checking, use GetIndexNameOrDefault instead.
func ResolveIndexName(ic *IndexConfig, envKey, defaultVal string) string {
	if ic != nil && ic.IndexName != "" {
		return ic.IndexName
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultVal
}

// ResolveCollectionName returns the collection name from IndexConfig, falling back to env var and then default.
// Priority: CollectionPrefix > CollectionName > env var > defaultVal.
// CollectionPrefix is checked first because Qdrant/Weaviate use it as the base name.
// CollectionName (Milvus) is checked second. If both are set, CollectionPrefix wins —
// this is safe because each VectorStore has a single engine type, so only one field is relevant.
func ResolveCollectionName(ic *IndexConfig, envKey, defaultVal string) string {
	if ic != nil {
		if ic.CollectionPrefix != "" {
			return ic.CollectionPrefix
		}
		if ic.CollectionName != "" {
			return ic.CollectionName
		}
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultVal
}

// OptionalUint32 converts int to *uint32 for Qdrant SDK.
// Returns nil for values <= 0, which tells Qdrant to use its server default.
func OptionalUint32(v int) *uint32 {
	if v <= 0 {
		return nil
	}
	u := uint32(v)
	return &u
}

// ---------------------------------------------------------------------------
// IndexConfig — validation
// ---------------------------------------------------------------------------

const (
	// maxShards is the upper bound for shard-related configuration values.
	maxShards = 64
	// maxReplicas is the upper bound for replication-related configuration values.
	maxReplicas = 10
)

// validIndexNamePattern restricts index/collection names to safe characters.
// Must start with a letter, followed by alphanumeric, underscore, or hyphen. Max 128 chars.
var validIndexNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,127}$`)

// ValidateIndexConfig checks IndexConfig fields for safe values.
// Call this from the service layer before persisting a VectorStore.
func ValidateIndexConfig(ic IndexConfig) error {
	// Validate string fields (index/collection names)
	if ic.IndexName != "" && !validIndexNamePattern.MatchString(ic.IndexName) {
		return errors.NewValidationError(
			"index_name must start with a letter and contain only alphanumeric, underscore, or hyphen characters (max 128)")
	}
	if ic.CollectionPrefix != "" && !validIndexNamePattern.MatchString(ic.CollectionPrefix) {
		return errors.NewValidationError(
			"collection_prefix must start with a letter and contain only alphanumeric, underscore, or hyphen characters (max 128)")
	}
	if ic.CollectionName != "" && !validIndexNamePattern.MatchString(ic.CollectionName) {
		return errors.NewValidationError(
			"collection_name must start with a letter and contain only alphanumeric, underscore, or hyphen characters (max 128)")
	}

	// Validate numeric fields (shards/replicas) — must be within safe bounds
	if ic.NumberOfShards < 0 || ic.NumberOfShards > maxShards {
		return errors.NewValidationError(fmt.Sprintf("number_of_shards must be between 0 and %d", maxShards))
	}
	if ic.NumberOfReplicas < 0 || ic.NumberOfReplicas > maxReplicas {
		return errors.NewValidationError(fmt.Sprintf("number_of_replicas must be between 0 and %d", maxReplicas))
	}
	if ic.ShardNumber < 0 || ic.ShardNumber > maxShards {
		return errors.NewValidationError(fmt.Sprintf("shard_number must be between 0 and %d", maxShards))
	}
	if ic.ReplicationFactor < 0 || ic.ReplicationFactor > maxReplicas {
		return errors.NewValidationError(fmt.Sprintf("replication_factor must be between 0 and %d", maxReplicas))
	}
	if ic.ShardsNum < 0 || ic.ShardsNum > maxShards {
		return errors.NewValidationError(fmt.Sprintf("shards_num must be between 0 and %d", maxShards))
	}
	if ic.ReplicaNumber < 0 || ic.ReplicaNumber > maxReplicas {
		return errors.NewValidationError(fmt.Sprintf("replica_number must be between 0 and %d", maxReplicas))
	}
	if ic.DesiredShardCount < 0 || ic.DesiredShardCount > maxShards {
		return errors.NewValidationError(fmt.Sprintf("desired_shard_count must be between 0 and %d", maxShards))
	}
	if ic.BucketsNum < 0 || ic.BucketsNum > maxShards {
		return errors.NewValidationError(fmt.Sprintf("buckets_num must be between 0 and %d", maxShards))
	}
	if ic.ReplicationNum < 0 || ic.ReplicationNum > maxReplicas {
		return errors.NewValidationError(fmt.Sprintf("replication_num must be between 0 and %d", maxReplicas))
	}

	return nil
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
			DisplayName: "Elasticsearch",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "URL", Default: "http://localhost:9200"},
				{Name: "username", Type: "string", Required: false, Description: "Username", Default: "elastic"},
				{Name: "password", Type: "string", Required: false, Sensitive: true, Description: "Password"},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "index_name", Type: "string", Required: false, Description: "Index Name", Default: "weknora"},
				{Name: "number_of_shards", Type: "number", Required: false, Description: "Shards", Default: 4},
				{Name: "number_of_replicas", Type: "number", Required: false, Description: "Replicas", Default: 1},
			},
		},
		// PostgreSQL and SQLite are excluded from the type list because they only support
		// the app's default DB connection (UseDefaultConnection=true). They appear as
		// env stores when configured via RETRIEVE_DRIVER but cannot be added as DB stores.
		{
			Type:        "qdrant",
			DisplayName: "Qdrant",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "host", Type: "string", Required: true, Description: "Host", Default: "localhost"},
				{Name: "port", Type: "number", Required: false, Description: "Port", Default: 6334},
				{Name: "api_key", Type: "string", Required: false, Sensitive: true, Description: "API Key"},
				{Name: "use_tls", Type: "boolean", Required: false, Description: "Use TLS", Default: false},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_prefix", Type: "string", Required: false, Description: "Collection Prefix", Default: "weknora_embeddings"},
				{Name: "shard_number", Type: "number", Required: false, Description: "Shard Number", Default: 1},
				{Name: "replication_factor", Type: "number", Required: false, Description: "Replication Factor", Default: 1},
			},
		},
		{
			Type:        "milvus",
			DisplayName: "Milvus",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "Address", Default: "localhost:19530"},
				{Name: "username", Type: "string", Required: false, Description: "Username", Default: "root"},
				{Name: "password", Type: "string", Required: false, Sensitive: true, Description: "Password"},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_name", Type: "string", Required: false, Description: "Collection Name", Default: "weknora_embeddings"},
				{Name: "shards_num", Type: "number", Required: false, Description: "Shards (write parallelism)", Default: 1},
				{Name: "replica_number", Type: "number", Required: false, Description: "In-memory Replicas (read HA)", Default: 1},
			},
		},
		{
			Type:        "tencent_vectordb",
			DisplayName: "Tencent VectorDB",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "Address", Default: "http://localhost:8080"},
				{Name: "username", Type: "string", Required: true, Description: "Username"},
				{Name: "api_key", Type: "string", Required: true, Sensitive: true, Description: "API Key"},
				{Name: "database", Type: "string", Required: false, Description: "Database", Default: "weknora"},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_name", Type: "string", Required: false, Description: "Collection Name", Default: "weknora_embeddings"},
				{Name: "shards_num", Type: "number", Required: false, Description: "Shards", Default: 1},
				{Name: "replica_number", Type: "number", Required: false, Description: "Replicas", Default: 1},
			},
		},
		{
			Type:        "weaviate",
			DisplayName: "Weaviate",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "host", Type: "string", Required: true, Description: "Host", Default: "weaviate:8080"},
				{Name: "grpc_address", Type: "string", Required: false, Description: "gRPC Address", Default: "weaviate:50051"},
				{Name: "scheme", Type: "string", Required: false, Description: "Scheme", Default: "http"},
				{Name: "api_key", Type: "string", Required: false, Sensitive: true, Description: "API Key"},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_prefix", Type: "string", Required: false, Description: "Collection Prefix", Default: "Weknora_embeddings"},
				{Name: "desired_shard_count", Type: "number", Required: false, Description: "Shard Count", Default: 1},
				{Name: "replication_factor", Type: "number", Required: false, Description: "Replication Factor", Default: 1},
			},
		},
		{
			Type:        "doris",
			DisplayName: "Apache Doris",
			ConnectionFields: []VectorStoreFieldInfo{
				{Name: "addr", Type: "string", Required: true, Description: "FE MySQL Address (host:port)", Default: "doris-fe:9030"},
				{Name: "http_port", Type: "number", Required: false, Description: "FE HTTP Port (Stream Load)", Default: 8030},
				{Name: "database", Type: "string", Required: true, Description: "Database", Default: "weknora"},
				{Name: "username", Type: "string", Required: false, Description: "Username", Default: "root"},
				{Name: "password", Type: "string", Required: false, Sensitive: true, Description: "Password"},
			},
			IndexFields: []VectorStoreFieldInfo{
				{Name: "collection_prefix", Type: "string", Required: false, Description: "Table Prefix", Default: "weknora_embeddings"},
				{Name: "buckets_num", Type: "number", Required: false, Description: "Buckets per table", Default: 10},
				{Name: "replication_num", Type: "number", Required: false, Description: "Replication Num", Default: 1},
			},
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
			Name:       "PostgreSQL",
			EngineType: PostgresRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				UseDefaultConnection: true,
			},
		}
	case "sqlite":
		return &VectorStore{
			ID:         "__env_sqlite__",
			Name:       "SQLite",
			EngineType: SQLiteRetrieverEngineType,
		}
	case "elasticsearch_v8":
		return &VectorStore{
			ID:         "__env_elasticsearch_v8__",
			Name:       "Elasticsearch v8",
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
			Name:       "Elasticsearch v7",
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
			Name:       "Qdrant",
			EngineType: QdrantRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Host:   envLookup("QDRANT_HOST"),
				APIKey: envLookup("QDRANT_API_KEY"),
			},
		}
	case "milvus":
		return &VectorStore{
			ID:         "__env_milvus__",
			Name:       "Milvus",
			EngineType: MilvusRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("MILVUS_ADDRESS"),
				Username: envLookup("MILVUS_USERNAME"),
				Password: envLookup("MILVUS_PASSWORD"),
			},
		}
	case "tencent_vectordb":
		return &VectorStore{
			ID:         "__env_tencent_vectordb__",
			Name:       "Tencent VectorDB",
			EngineType: TencentVectorDBRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("TENCENT_VECTORDB_ADDR"),
				Username: envLookup("TENCENT_VECTORDB_USERNAME"),
				APIKey:   envLookup("TENCENT_VECTORDB_API_KEY"),
				Database: envLookup("TENCENT_VECTORDB_DATABASE"),
			},
			IndexConfig: IndexConfig{
				CollectionName: envLookup("TENCENT_VECTORDB_COLLECTION"),
			},
		}
	case "weaviate":
		return &VectorStore{
			ID:         "__env_weaviate__",
			Name:       "Weaviate",
			EngineType: WeaviateRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Host:        envLookup("WEAVIATE_HOST"),
				GrpcAddress: envLookup("WEAVIATE_GRPC_ADDRESS"),
				Scheme:      envLookup("WEAVIATE_SCHEME"),
				APIKey:      envLookup("WEAVIATE_API_KEY"),
			},
		}
	case "doris":
		httpPort := 0
		if v := envLookup("DORIS_HTTP_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				httpPort = p
			}
		}
		return &VectorStore{
			ID:         "__env_doris__",
			Name:       "Apache Doris",
			EngineType: DorisRetrieverEngineType,
			ConnectionConfig: ConnectionConfig{
				Addr:     envLookup("DORIS_ADDR"),
				HTTPPort: httpPort,
				Database: envLookup("DORIS_DATABASE"),
				Username: envLookup("DORIS_USERNAME"),
				Password: envLookup("DORIS_PASSWORD"),
			},
			IndexConfig: IndexConfig{
				CollectionPrefix: envLookup("DORIS_TABLE_PREFIX"),
			},
		}
	default:
		return nil
	}
}
