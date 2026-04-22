package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/skills"
)

// SkillService defines the interface for skill service
type SkillService interface {
	// ListPreloadedSkills returns metadata for all preloaded skills
	ListPreloadedSkills(ctx context.Context) ([]*skills.SkillMetadata, error)

	// GetSkillByName returns skill by name
	GetSkillByName(ctx context.Context, name string) (*skills.Skill, error)

	// --- Skills Hub extension interfaces ---

	// ListAllSkills returns summaries for all skills (preloaded and installed)
	ListAllSkills(ctx context.Context) ([]skills.Summary, error)

	// GetSkillDetail returns full details for a skill
	GetSkillDetail(ctx context.Context, name string) (*skills.FullSkill, error)

	// InstallSkillFromURL installs a skill from a URL
	InstallSkillFromURL(ctx context.Context, name string, url string) error

	// InstallSkillFromUpload installs a skill from an uploaded file
	InstallSkillFromUpload(ctx context.Context, name string, data []byte, filename string) error

	// UninstallSkill uninstalls a skill
	UninstallSkill(ctx context.Context, name string) error

	// RefreshSkills refreshes the skill index
	RefreshSkills(ctx context.Context) error

	// ExportSkill exports a skill as a zip archive
	ExportSkill(ctx context.Context, name string) ([]byte, error)

	// ListSkillFiles lists all files in the skill directory
	ListSkillFiles(ctx context.Context, name string) ([]string, error)

	// GetSkillFile gets the content of a specific file in the skill directory
	GetSkillFile(ctx context.Context, name string, filePath string) ([]byte, string, error)

	// --- Artifact Service ---

	// SaveArtifact saves an artifact
	SaveArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, artifact *skills.Artifact) (int, error)

	// LoadArtifact loads an artifact
	LoadArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, version *int) (*skills.Artifact, error)

	// ListArtifacts lists all artifacts in the session
	ListArtifacts(ctx context.Context, sessionInfo skills.ArtifactSessionInfo) ([]skills.ArtifactMeta, error)

	// DeleteArtifact deletes an artifact
	DeleteArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string) error

	// ExportArtifact exports an artifact (returns data and MIME type)
	ExportArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, version *int) ([]byte, string, error)

	// GetArtifactService returns the artifact service instance (for tool integration)
	GetArtifactService() skills.ArtifactService

	// GetPreloadedDir returns the preloaded skills directory
	GetPreloadedDir() string
}
