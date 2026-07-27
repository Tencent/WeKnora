package skillpkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sys/unix"
)

type FileStorage struct {
	root      string
	validator *Validator
}

type Storage interface {
	Stage(ctx context.Context, tenantID uint64, uploadID string, archive io.Reader, archiveSize int64) (*ValidatedPackage, error)
	Materialize(ctx context.Context, tenantID uint64, skillID, versionID string, pkg *ValidatedPackage) (string, string, error)
	VerifyVersion(ctx context.Context, tenantID uint64, version *types.TenantSkillVersion) error
	RemoveVersion(ctx context.Context, tenantID uint64, version *types.TenantSkillVersion) error
	Reconcile(ctx context.Context, olderThan time.Time) error
}

func NewFileStorage(root string, validator *Validator) *FileStorage {
	return &FileStorage{root: filepath.Clean(root), validator: validator}
}

func (s *FileStorage) Stage(
	ctx context.Context,
	tenantID uint64,
	uploadID string,
	archive io.Reader,
	archiveSize int64,
) (*ValidatedPackage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !safeStorageID(uploadID) {
		return nil, &PackageError{Code: "invalid_upload_id"}
	}
	pkg, err := s.validator.Validate(archive, archiveSize)
	if err != nil {
		return nil, err
	}
	pkg.tenantID = tenantID
	pkg.uploadID = uploadID
	return pkg, nil
}

func (s *FileStorage) Materialize(
	ctx context.Context,
	tenantID uint64,
	skillID string,
	versionID string,
	pkg *ValidatedPackage,
) (string, string, error) {
	if pkg == nil || pkg.tenantID != tenantID || !safeStorageID(skillID) || !safeStorageID(versionID) {
		return "", "", &PackageError{Code: "invalid_storage_identity"}
	}
	stagingRel := filepath.Join(".staging", strconv.FormatUint(tenantID, 10), pkg.uploadID)
	staging, err := s.resolveWithinRoot(stagingRel)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		return "", "", err
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return "", "", fmt.Errorf("create staging: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staging)
		}
	}()
	for _, file := range pkg.Files {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if err := writeValidatedFile(staging, file); err != nil {
			return "", "", err
		}
	}
	contentHash, err := hashDirectory(staging)
	if err != nil {
		return "", "", err
	}
	targetRel := filepath.Join(strconv.FormatUint(tenantID, 10), skillID, versionID)
	target, err := s.resolveWithinRoot(targetRel)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", "", err
	}
	if _, err := os.Lstat(target); err == nil {
		return "", "", &PackageError{Code: "version_path_exists", Path: targetRel}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", "", fmt.Errorf("materialize version: %w", err)
	}
	cleanup = false
	return targetRel, contentHash, nil
}

func (s *FileStorage) VerifyVersion(
	ctx context.Context,
	tenantID uint64,
	version *types.TenantSkillVersion,
) error {
	if version == nil || version.TenantID != tenantID {
		return &PackageError{Code: "storage_path_mismatch"}
	}
	expected := filepath.Join(strconv.FormatUint(tenantID, 10), version.SkillID, version.ID)
	if filepath.Clean(version.StoragePath) != expected {
		return &PackageError{Code: "storage_path_mismatch", Path: version.StoragePath}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.resolveWithinRoot(expected)
	if err != nil {
		return err
	}
	actual, err := hashDirectory(dir)
	if err != nil {
		return err
	}
	if actual != version.ContentHash {
		return &PackageError{Code: "content_hash_mismatch", Path: version.StoragePath}
	}
	return nil
}

func (s *FileStorage) RemoveVersion(
	ctx context.Context,
	tenantID uint64,
	version *types.TenantSkillVersion,
) error {
	if err := s.VerifyVersion(ctx, tenantID, version); err != nil {
		return err
	}
	dir, err := s.resolveWithinRoot(version.StoragePath)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (s *FileStorage) Reconcile(ctx context.Context, olderThan time.Time) error {
	staging, err := s.resolveWithinRoot(".staging")
	if err != nil {
		return err
	}
	tenantDirs, err := os.ReadDir(staging)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, tenantDir := range tenantDirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !tenantDir.IsDir() {
			continue
		}
		uploadsPath := filepath.Join(staging, tenantDir.Name())
		uploads, err := os.ReadDir(uploadsPath)
		if err != nil {
			return err
		}
		for _, upload := range uploads {
			if !upload.IsDir() {
				continue
			}
			info, err := upload.Info()
			if err != nil {
				return err
			}
			if info.ModTime().Before(olderThan) {
				if err := os.RemoveAll(filepath.Join(uploadsPath, upload.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *FileStorage) resolveWithinRoot(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", &PackageError{Code: "storage_path_mismatch", Path: relative}
	}
	target := filepath.Join(s.root, filepath.Clean(relative))
	rel, err := filepath.Rel(s.root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &PackageError{Code: "storage_path_mismatch", Path: relative}
	}
	return target, nil
}

var storageIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

func safeStorageID(value string) bool { return storageIDPattern.MatchString(value) }

func writeValidatedFile(root string, file ValidatedFile) error {
	target := filepath.Join(root, filepath.FromSlash(file.Path))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return &PackageError{Code: "storage_path_mismatch", Path: file.Path}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	fd, err := unix.Open(target, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("create skill file: %w", err)
	}
	handle := os.NewFile(uintptr(fd), target)
	if _, err := handle.Write(file.Data); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}

func hashDirectory(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return &PackageError{Code: "unsupported_entry_type", Path: name}
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(hash, filepath.ToSlash(relative)); err != nil {
			return err
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return err
		}
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
