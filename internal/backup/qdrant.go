package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const defaultQdrantSnapshotPort = "6333"

// QdrantSnapshot describes one native Qdrant collection snapshot copied into
// BACKUP_LOCAL_DIR. It never includes an endpoint, API key, or absolute path.
type QdrantSnapshot struct {
	Collection  string    `json:"collection"`
	File        string    `json:"file"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type qdrantSnapshotter interface {
	Create(context.Context, MySQLConfig, string) ([]QdrantSnapshot, error)
}

type qdrantHTTPClient struct {
	client *http.Client
	now    func() time.Time
}

func (c qdrantHTTPClient) Create(ctx context.Context, config MySQLConfig, backupID string) ([]QdrantSnapshot, error) {
	base, err := url.Parse(config.QdrantURL)
	if err != nil {
		return nil, err
	}
	collections, err := c.listCollections(ctx, base, config.QdrantAPIKey)
	if err != nil {
		return nil, err
	}
	result := make([]QdrantSnapshot, 0, len(collections))
	for _, collection := range collections {
		startedAt := c.now().UTC()
		snapshotName, err := c.createSnapshot(ctx, base, config.QdrantAPIKey, collection)
		if err != nil {
			removeQdrantSnapshots(config.LocalDir, result)
			return nil, err
		}
		snapshot, err := c.downloadSnapshot(ctx, base, config.QdrantAPIKey, collection, snapshotName, config.LocalDir, backupID, startedAt)
		deleteErr := c.deleteSnapshot(ctx, base, config.QdrantAPIKey, collection, snapshotName)
		if err != nil || deleteErr != nil {
			if err == nil {
				err = deleteErr
			}
			removeQdrantSnapshots(config.LocalDir, append(result, snapshot))
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func (c qdrantHTTPClient) listCollections(ctx context.Context, base *url.URL, apiKey string) ([]string, error) {
	var response struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodGet, qdrantURL(base, "collections"), apiKey, nil, &response); err != nil {
		return nil, err
	}
	collections := make([]string, 0, len(response.Result.Collections))
	for _, item := range response.Result.Collections {
		if !validQdrantName(item.Name) {
			return nil, errors.New("qdrant collection name invalid")
		}
		collections = append(collections, item.Name)
	}
	return collections, nil
}

func (c qdrantHTTPClient) createSnapshot(ctx context.Context, base *url.URL, apiKey, collection string) (string, error) {
	var response struct {
		Result struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, qdrantURL(base, "collections", collection, "snapshots"), apiKey, nil, &response); err != nil || !validQdrantName(response.Result.Name) {
		return "", errors.New("qdrant snapshot creation failed")
	}
	return response.Result.Name, nil
}

func (c qdrantHTTPClient) downloadSnapshot(ctx context.Context, base *url.URL, apiKey, collection, snapshotName, directory, backupID string, startedAt time.Time) (QdrantSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, qdrantURL(base, "collections", collection, "snapshots", snapshotName), nil)
	if err != nil {
		return QdrantSnapshot{}, err
	}
	setQdrantAPIKey(request, apiKey)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return QdrantSnapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return QdrantSnapshot{}, errors.New("qdrant snapshot download failed")
	}
	temporary, err := os.CreateTemp(directory, ".weknora-qdrant-*.snapshot.partial")
	if err != nil {
		return QdrantSnapshot{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return QdrantSnapshot{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hash), response.Body)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || size <= 0 {
		return QdrantSnapshot{}, errors.New("qdrant snapshot write failed")
	}
	filename := qdrantSnapshotFileName(backupID, collection)
	if err := os.Rename(temporaryPath, filepath.Join(directory, filename)); err != nil {
		return QdrantSnapshot{}, err
	}
	return QdrantSnapshot{Collection: collection, File: filename, SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil)), StartedAt: startedAt, CompletedAt: c.now().UTC()}, nil
}

func (c qdrantHTTPClient) deleteSnapshot(ctx context.Context, base *url.URL, apiKey, collection, snapshotName string) error {
	return c.doJSON(ctx, http.MethodDelete, qdrantURL(base, "collections", collection, "snapshots", snapshotName), apiKey, nil, nil)
}

func (c qdrantHTTPClient) doJSON(ctx context.Context, method, endpoint, apiKey string, body io.Reader, destination any) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	setQdrantAPIKey(request, apiKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("qdrant request failed")
	}
	if destination != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(destination)
	}
	return nil
}

func (c qdrantHTTPClient) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func qdrantURL(base *url.URL, elements ...string) string {
	copy := *base
	segments := make([]string, 0, len(elements)+1)
	if trimmed := strings.Trim(copy.Path, "/"); trimmed != "" {
		segments = append(segments, trimmed)
	}
	segments = append(segments, elements...)
	copy.Path = "/" + path.Join(segments...)
	return copy.String()
}

func setQdrantAPIKey(request *http.Request, apiKey string) {
	if apiKey != "" {
		request.Header.Set("api-key", apiKey)
	}
}

func validQdrantName(name string) bool {
	if name == "" || len(name) > 255 || name == "." || name == ".." {
		return false
	}
	for _, character := range name {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

func qdrantSnapshotFileName(backupID, collection string) string {
	sum := sha256.Sum256([]byte(collection))
	return backupID + ".qdrant." + hex.EncodeToString(sum[:8]) + ".snapshot"
}

func removeQdrantSnapshots(directory string, snapshots []QdrantSnapshot) {
	for _, snapshot := range snapshots {
		_ = os.Remove(filepath.Join(directory, snapshot.File))
	}
}

// VerifyQdrantSnapshot verifies a downloaded native Qdrant snapshot without
// importing it into a live vector store.
func VerifyQdrantSnapshot(path string, snapshot QdrantSnapshot) error {
	return VerifyArchive(path, Archive{File: snapshot.File, SizeBytes: snapshot.SizeBytes, SHA256: snapshot.SHA256})
}
