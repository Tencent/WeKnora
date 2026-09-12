// Package publicdata exposes the fixed, source-attributed evaluation bundles
// compiled into the service binary.
package publicdata

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed */v1/manifest.json */v1/registry-input.json
var artifacts embed.FS

// ErrNotFound identifies a catalog id outside the fixed evaluation bundle set.
var ErrNotFound = errors.New("public evaluation dataset not found")

var catalogIDs = [...]string{"cmrc2018-dev", "squad2-dev"}

// Item describes an attributable subset, including its evaluation limitations.
type Item struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Language    string         `json:"language"`
	SourceURL   string         `json:"source_url"`
	License     string         `json:"license"`
	Counts      map[string]int `json:"counts"`
	Limitations []string       `json:"limitations"`
}

// Bundle is ready for the tenant import endpoint; Manifest preserves provenance.
type Bundle struct {
	ID          string                               `json:"id"`
	Name        string                               `json:"name"`
	Description string                               `json:"description"`
	Content     *types.EvaluationDatasetVersionInput `json:"content"`
	Manifest    json.RawMessage                      `json:"manifest"`
}

// List verifies every fixed evaluation artifact before returning catalog items.
func List() ([]Item, error) {
	return list(artifacts)
}

func list(source fs.FS) ([]Item, error) {
	items := make([]Item, 0, len(catalogIDs))
	for _, id := range catalogIDs {
		item, _, err := readBundle(source, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, errors.New("public evaluation catalog is empty")
	}
	return items, nil
}

// Get reads only a fixed embedded bundle, with no filesystem or URL fallback.
func Get(id string) (*Bundle, error) {
	for _, allowed := range catalogIDs {
		if id == allowed {
			_, bundle, err := readBundle(artifacts, id)
			return bundle, err
		}
	}
	return nil, ErrNotFound
}

func readBundle(source fs.FS, id string) (Item, *Bundle, error) {
	var manifest struct {
		Item
		Kind        string            `json:"kind"`
		FilesSHA256 map[string]string `json:"files_sha256"`
	}
	base := id + "/v1/"
	rawManifest, err := fs.ReadFile(source, base+"manifest.json")
	if err != nil {
		return Item{}, nil, fmt.Errorf("read public dataset %s manifest: %w", id, err)
	}
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return Item{}, nil, fmt.Errorf("decode public dataset %s manifest: %w", id, err)
	}
	if manifest.ID != id || manifest.Kind != "evaluation" || manifest.Name == "" ||
		manifest.Language == "" || manifest.SourceURL == "" || manifest.License == "" {
		return Item{}, nil, fmt.Errorf("public dataset %s has an invalid manifest", id)
	}
	rawContent, err := fs.ReadFile(source, base+"registry-input.json")
	if err != nil {
		return Item{}, nil, fmt.Errorf("read public dataset %s content: %w", id, err)
	}
	if types.EvaluationDatasetArtifactSHA256(rawContent) != manifest.FilesSHA256["registry-input.json"] {
		return Item{}, nil, fmt.Errorf("public dataset %s artifact SHA-256 mismatch", id)
	}
	var content types.EvaluationDatasetVersionInput
	if err := json.Unmarshal(rawContent, &content); err != nil {
		return Item{}, nil, fmt.Errorf("decode public dataset %s content: %w", id, err)
	}
	if len(content.Passages) == 0 || len(content.Questions) == 0 || content.Relevance == nil ||
		len(content.Passages) != manifest.Counts["passages"] ||
		len(content.Questions) != manifest.Counts["questions"] ||
		len(content.Relevance) != manifest.Counts["relevance"] {
		return Item{}, nil, fmt.Errorf("public dataset %s content counts do not match its manifest", id)
	}
	return manifest.Item, &Bundle{
		ID: id, Name: manifest.Name, Description: manifest.Description,
		Content: &content, Manifest: rawManifest,
	}, nil
}
