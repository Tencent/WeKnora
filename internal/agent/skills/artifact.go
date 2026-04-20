package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Artifact defines a skill artifact
type Artifact struct {
	// Data contains the raw byte data
	Data []byte `json:"data,omitempty"`
	// MimeType is the IANA standard MIME type of the data
	MimeType string `json:"mime_type,omitempty"`
	// URL is the optional access URL for the artifact
	URL string `json:"url,omitempty"`
	// Name is the optional display name for the artifact
	Name string `json:"name,omitempty"`
}

// ArtifactSessionInfo artifact session info
type ArtifactSessionInfo struct {
	// AppName application name
	AppName string `json:"app_name"`
	// UserID user ID
	UserID string `json:"user_id"`
	// SessionID session ID
	SessionID string `json:"session_id"`
}

// ArtifactMeta artifact meta
type ArtifactMeta struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Name     string `json:"name"`
	Versions int    `json:"versions"`
	Size     int64  `json:"size"`
}

// ArtifactService defines artifact service
type ArtifactService interface {
	// SaveArtifact saves artifact to storage
	SaveArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string, artifact *Artifact) (int, error)

	// LoadArtifact loads artifact from storage
	LoadArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string, version *int) (*Artifact, error)

	// ListArtifactKeys lists all artifact filenames in the session
	ListArtifactKeys(ctx context.Context, sessionInfo ArtifactSessionInfo) ([]string, error)

	// ListArtifacts lists all artifact metadata in the session
	ListArtifacts(ctx context.Context, sessionInfo ArtifactSessionInfo) ([]ArtifactMeta, error)

	// DeleteArtifact deletes artifact
	DeleteArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string) error

	// ListVersions lists all versions of an artifact
	ListVersions(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string) ([]int, error)
}

// InMemoryArtifactService in memory artifact service
type InMemoryArtifactService struct {
	mutex     sync.RWMutex
	artifacts map[string][]*Artifact
}

// NewInMemoryArtifactService creates a new in memory artifact service
func NewInMemoryArtifactService() *InMemoryArtifactService {
	return &InMemoryArtifactService{
		artifacts: make(map[string][]*Artifact),
	}
}

// buildArtifactPath builds artifact path
func buildArtifactPath(sessionInfo ArtifactSessionInfo, filename string) string {
	return fmt.Sprintf("%s/%s/%s/%s", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
}

// buildSessionPrefix builds session prefix
func buildSessionPrefix(sessionInfo ArtifactSessionInfo) string {
	return fmt.Sprintf("%s/%s/%s/", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID)
}

// SaveArtifact saves artifact to memory storage
func (s *InMemoryArtifactService) SaveArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string, art *Artifact) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	path := buildArtifactPath(sessionInfo, filename)
	logger.Infof(ctx, "saving artifact: %s to %s", filename, path)
	if s.artifacts[path] == nil {
		s.artifacts[path] = make([]*Artifact, 0)
	}

	version := len(s.artifacts[path])
	s.artifacts[path] = append(s.artifacts[path], art)

	return version, nil
}

// LoadArtifact loads artifact from memory storage
func (s *InMemoryArtifactService) LoadArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string, version *int) (*Artifact, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := buildArtifactPath(sessionInfo, filename)
	versions, exists := s.artifacts[path]
	logger.Infof(ctx, "loading artifact: %s from %s", filename, path)
	logger.Infof(ctx, "artifact exists: %v", s.artifacts)
	if !exists || len(versions) == 0 {
		return nil, nil
	}

	var versionIndex int
	if version == nil {
		versionIndex = len(versions) - 1
	} else {
		versionIndex = *version
		if versionIndex < 0 || versionIndex >= len(versions) {
			return nil, fmt.Errorf("version %d does not exist", *version)
		}
	}

	return versions[versionIndex], nil
}

// ListArtifactKeys lists all artifact keys in the session
func (s *InMemoryArtifactService) ListArtifactKeys(ctx context.Context, sessionInfo ArtifactSessionInfo) ([]string, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	sessionPrefix := buildSessionPrefix(sessionInfo)

	var filenames []string
	for path := range s.artifacts {
		if strings.HasPrefix(path, sessionPrefix) {
			filename := strings.TrimPrefix(path, sessionPrefix)
			filenames = append(filenames, filename)
		}
	}

	sort.Strings(filenames)
	return filenames, nil
}

// ListArtifacts lists all artifacts in the session
func (s *InMemoryArtifactService) ListArtifacts(ctx context.Context, sessionInfo ArtifactSessionInfo) ([]ArtifactMeta, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	sessionPrefix := buildSessionPrefix(sessionInfo)

	var metas []ArtifactMeta
	for path, versions := range s.artifacts {
		if strings.HasPrefix(path, sessionPrefix) {
			filename := strings.TrimPrefix(path, sessionPrefix)
			if len(versions) == 0 {
				continue
			}
			latest := versions[len(versions)-1]
			meta := ArtifactMeta{
				Filename: filename,
				MimeType: latest.MimeType,
				Name:     latest.Name,
				Versions: len(versions),
				Size:     int64(len(latest.Data)),
			}
			metas = append(metas, meta)
		}
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Filename < metas[j].Filename
	})
	return metas, nil
}

// DeleteArtifact deletes an artifact
func (s *InMemoryArtifactService) DeleteArtifact(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	path := buildArtifactPath(sessionInfo, filename)
	delete(s.artifacts, path)
	return nil
}

// ListVersions lists all versions of an artifact
func (s *InMemoryArtifactService) ListVersions(ctx context.Context, sessionInfo ArtifactSessionInfo, filename string) ([]int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := buildArtifactPath(sessionInfo, filename)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return []int{}, nil
	}

	result := make([]int, len(versions))
	for i := range versions {
		result[i] = i
	}

	return result, nil
}
