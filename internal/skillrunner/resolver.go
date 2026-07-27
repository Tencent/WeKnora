package skillrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileResolver struct{ root, sourceVolume string }

func NewFileResolver(root, sourceVolume string) *FileResolver {
	return &FileResolver{root: filepath.Clean(root), sourceVolume: sourceVolume}
}

func (resolver *FileResolver) Resolve(ctx context.Context, request ExecuteRequest) (ResolvedVersion, error) {
	if err := ValidateRequest(request); err != nil {
		return ResolvedVersion{}, err
	}
	root, err := filepath.Abs(resolver.root)
	if err != nil {
		return ResolvedVersion{}, err
	}
	source := filepath.Join(root, request.TenantID, request.SkillID, request.VersionID)
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return ResolvedVersion{}, ErrInvalidRequest
	}
	actual, err := hashVersion(ctx, source)
	if err != nil {
		return ResolvedVersion{}, err
	}
	if actual != request.ContentHash {
		return ResolvedVersion{}, fmt.Errorf("content hash mismatch")
	}
	if resolver.sourceVolume == "" {
		return ResolvedVersion{}, fmt.Errorf("source volume is required")
	}
	scripts, err := registeredScripts(source)
	if err != nil {
		return ResolvedVersion{}, err
	}
	return ResolvedVersion{SourcePath: source, ContentHash: actual, SourceVolume: resolver.sourceVolume, AllowedScripts: scripts}, nil
}

func registeredScripts(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" {
		return nil, ErrInvalidRequest
	}
	var manifest struct {
		Scripts []string `yaml:"scripts"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &manifest); err != nil {
		return nil, ErrInvalidRequest
	}
	for _, script := range manifest.Scripts {
		clean := filepath.ToSlash(filepath.Clean(script))
		if clean != script || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return nil, ErrInvalidRequest
		}
	}
	return manifest.Scripts, nil
}

func hashVersion(ctx context.Context, root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalidRequest
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		_, _ = hash.Write([]byte{0})
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
