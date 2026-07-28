package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const fileInventoryVersion = 1

// FileArchive records the bounded local-file snapshot that belongs to one
// MySQL backup. Names are relative to BACKUP_LOCAL_DIR; file paths inside the
// inventory are relative to LOCAL_STORAGE_BASE_DIR.
type FileArchive struct {
	File          string    `json:"file"`
	InventoryFile string    `json:"inventory_file"`
	SizeBytes     int64     `json:"size_bytes"`
	SHA256        string    `json:"sha256"`
	Compression   string    `json:"compression"`
	FileCount     int       `json:"file_count"`
	ContentBytes  int64     `json:"content_bytes"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
}

type fileInventory struct {
	FormatVersion int                 `json:"format_version"`
	BackupID      string              `json:"backup_id"`
	StartedAt     time.Time           `json:"started_at"`
	CompletedAt   time.Time           `json:"completed_at"`
	Files         []fileInventoryItem `json:"files"`
}

type fileInventoryItem struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func (m *MySQLManager) writeFilesArchive(ctx context.Context, backupID string, startedAt time.Time) (*FileArchive, error) {
	if err := ensureFileBackupSource(m.config.FilesDir, m.config.LocalDir); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(m.config.LocalDir, ".weknora-files-*.tar.gz.partial")
	if err != nil {
		return nil, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, err
	}

	archiveHash := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(temporary, archiveHash))
	writer := tar.NewWriter(compressed)
	inventory := fileInventory{FormatVersion: fileInventoryVersion, BackupID: backupID, StartedAt: startedAt}
	walkErr := filepath.Walk(m.config.FilesDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == m.config.FilesDir {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local storage contains symlink")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("local storage contains unsupported file")
		}
		relative, err := filepath.Rel(m.config.FilesDir, path)
		if err != nil || !validArchiveRelativePath(relative) {
			return errors.New("local storage path invalid")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		openedInfo, err := input.Stat()
		if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
			_ = input.Close()
			return errors.New("local storage file changed during backup")
		}
		header, err := tar.FileInfoHeader(openedInfo, "")
		if err != nil {
			_ = input.Close()
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Mode = 0o600
		if err := writer.WriteHeader(header); err != nil {
			_ = input.Close()
			return err
		}
		contentHash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(writer, contentHash), input)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil || written != openedInfo.Size() {
			return errors.New("local storage file copy failed")
		}
		inventory.Files = append(inventory.Files, fileInventoryItem{
			Path: relative, SizeBytes: written, SHA256: hex.EncodeToString(contentHash.Sum(nil)),
		})
		return nil
	})
	closeErr := writer.Close()
	gzipErr := compressed.Close()
	syncErr := temporary.Sync()
	fileErr := temporary.Close()
	if walkErr != nil || closeErr != nil || gzipErr != nil || syncErr != nil || fileErr != nil {
		return nil, errors.New("local file archive write failed")
	}
	info, err := os.Stat(temporaryPath)
	if err != nil {
		return nil, err
	}
	sort.Slice(inventory.Files, func(i, j int) bool { return inventory.Files[i].Path < inventory.Files[j].Path })
	inventory.CompletedAt = m.now().UTC()
	archive := &FileArchive{
		File: backupID + ".files.tar.gz", InventoryFile: backupID + ".files.json",
		SizeBytes: info.Size(), SHA256: hex.EncodeToString(archiveHash.Sum(nil)), Compression: "gzip",
		FileCount: len(inventory.Files), StartedAt: startedAt, CompletedAt: inventory.CompletedAt,
	}
	for _, item := range inventory.Files {
		archive.ContentBytes += item.SizeBytes
	}
	archivePath := filepath.Join(m.config.LocalDir, archive.File)
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return nil, err
	}
	if err := writeFileInventory(m.config.LocalDir, archive.InventoryFile, inventory); err != nil {
		_ = os.Remove(archivePath)
		return nil, err
	}
	return archive, nil
}

func ensureFileBackupSource(source, destination string) error {
	if !filepath.IsAbs(source) || filepath.Clean(source) == string(filepath.Separator) || pathsOverlap(source, destination) {
		return errors.New("local file backup path invalid")
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return errors.New("local storage directory unavailable")
	}
	return nil
}

func pathsOverlap(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	for _, pair := range [][2]string{{first, second}, {second, first}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func validArchiveRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func writeFileInventory(directory, name string, inventory fileInventory) error {
	contents, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	temporary, err := os.CreateTemp(directory, ".weknora-files-*.json.partial")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(directory, name))
}

// VerifyFileArchive verifies the compressed archive and every inventory entry
// without extracting into the live LOCAL_STORAGE_BASE_DIR.
func VerifyFileArchive(archivePath, inventoryPath string, archive FileArchive) error {
	if err := VerifyArchive(archivePath, Archive{File: archive.File, SizeBytes: archive.SizeBytes, SHA256: archive.SHA256, Compression: archive.Compression}); err != nil {
		return err
	}
	contents, err := os.ReadFile(inventoryPath)
	if err != nil {
		return &Error{Kind: ErrorStorage}
	}
	var inventory fileInventory
	if json.Unmarshal(contents, &inventory) != nil || inventory.FormatVersion != fileInventoryVersion || inventory.BackupID == "" || len(inventory.Files) != archive.FileCount {
		return &Error{Kind: ErrorIntegrity}
	}
	expected := make(map[string]fileInventoryItem, len(inventory.Files))
	for _, item := range inventory.Files {
		if !validArchiveRelativePath(item.Path) || item.SizeBytes < 0 || item.SHA256 == "" {
			return &Error{Kind: ErrorIntegrity}
		}
		if _, found := expected[item.Path]; found {
			return &Error{Kind: ErrorIntegrity}
		}
		expected[item.Path] = item
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return &Error{Kind: ErrorStorage}
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return &Error{Kind: ErrorIntegrity}
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var contentBytes int64
	seen := make(map[string]bool, len(expected))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg || !validArchiveRelativePath(filepath.FromSlash(header.Name)) {
			return &Error{Kind: ErrorIntegrity}
		}
		path := filepath.FromSlash(header.Name)
		item, found := expected[path]
		if !found || seen[path] || header.Size != item.SizeBytes {
			return &Error{Kind: ErrorIntegrity}
		}
		hash := sha256.New()
		written, err := io.Copy(hash, reader)
		if err != nil || written != item.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
			return &Error{Kind: ErrorIntegrity}
		}
		seen[path] = true
		contentBytes += written
	}
	if len(seen) != len(expected) || contentBytes != archive.ContentBytes {
		return &Error{Kind: ErrorIntegrity}
	}
	return nil
}
