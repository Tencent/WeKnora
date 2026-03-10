package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// SystemInfo represents system version and configuration information
type SystemInfo struct {
	Version             string `json:"version"`
	Edition             string `json:"edition"`
	CommitID            string `json:"commit_id,omitempty"`
	BuildTime           string `json:"build_time,omitempty"`
	GoVersion           string `json:"go_version,omitempty"`
	KeywordIndexEngine  string `json:"keyword_index_engine,omitempty"`
	VectorStoreEngine   string `json:"vector_store_engine,omitempty"`
	GraphDatabaseEngine string `json:"graph_database_engine,omitempty"`
	MinioEnabled        bool   `json:"minio_enabled,omitempty"`
	DBVersion           string `json:"db_version,omitempty"`
}

// ParserEngine represents a document parser engine
type ParserEngine struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

// StorageEngineStatusItem describes one storage engine's availability
type StorageEngineStatusItem struct {
	Name        string `json:"name"`
	Available   bool   `json:"available"`
	Description string `json:"description"`
}

// StorageEngineStatusResponse is the response for storage engine status
type StorageEngineStatusResponse struct {
	Engines           []StorageEngineStatusItem `json:"engines"`
	MinioEnvAvailable bool                      `json:"minio_env_available"`
}

// StorageCheckRequest is the body for storage engine connectivity check
type StorageCheckRequest struct {
	Provider string          `json:"provider"`
	MinIO    json.RawMessage `json:"minio,omitempty"`
	COS      json.RawMessage `json:"cos,omitempty"`
	TOS      json.RawMessage `json:"tos,omitempty"`
	S3       json.RawMessage `json:"s3,omitempty"`
}

// StorageCheckResponse is the response for storage engine check
type StorageCheckResponse struct {
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
	BucketCreated bool   `json:"bucket_created,omitempty"`
}

// MinioBucketInfo represents MinIO bucket information
type MinioBucketInfo struct {
	Name      string `json:"name"`
	Policy    string `json:"policy"`
	CreatedAt string `json:"created_at,omitempty"`
}

// GetSystemInfo gets system version and configuration information
func (c *Client) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/system/info", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code int         `json:"code"`
		Data *SystemInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListParserEngines lists available document parser engines
func (c *Client) ListParserEngines(ctx context.Context) ([]ParserEngine, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/system/parser-engines", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code      int            `json:"code"`
		Data      []ParserEngine `json:"data"`
		Connected bool           `json:"connected"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckParserEngines checks parser engine availability with given config overrides
func (c *Client) CheckParserEngines(ctx context.Context, config any) ([]ParserEngine, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/system/parser-engines/check", config, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code int            `json:"code"`
		Data []ParserEngine `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ReconnectDocReader reconnects the document parser service to a new address
func (c *Client) ReconnectDocReader(ctx context.Context, addr string) error {
	req := map[string]string{"addr": addr}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/system/docreader/reconnect", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// GetStorageEngineStatus gets the availability status of all storage engines
func (c *Client) GetStorageEngineStatus(ctx context.Context) (*StorageEngineStatusResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/system/storage-engine-status", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code int                          `json:"code"`
		Data *StorageEngineStatusResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckStorageEngine tests connectivity for a storage engine
func (c *Client) CheckStorageEngine(ctx context.Context, req *StorageCheckRequest) (*StorageCheckResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/system/storage-engine-check", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code int                   `json:"code"`
		Data *StorageCheckResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListMinioBuckets lists all MinIO buckets with their access policies
func (c *Client) ListMinioBuckets(ctx context.Context) ([]MinioBucketInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/system/minio/buckets", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			Buckets []MinioBucketInfo `json:"buckets"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Buckets, nil
}
