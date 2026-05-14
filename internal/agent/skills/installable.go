package skills

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxDownloadBytes     = 64 * 1024 * 1024  // 64 MiB
	maxExtractFileBytes  = 64 * 1024 * 1024  // 64 MiB
	maxExtractTotalBytes = 256 * 1024 * 1024 // 256 MiB
	dirPerm              = 0o755
	filePerm             = 0o644
)

// UserInstallableRepository supports installing/uninstalling skills
type UserInstallableRepository struct {
	*FSRepository
	installDir string // directory where skills are installed
}

// NewUserInstallableRepository creates a repository that supports installing/uninstalling skills
// preloadedDirs: list of preloaded skill directories
// installDir: directory where skills are installed
func NewUserInstallableRepository(preloadedDirs []string, installDir string) (*UserInstallableRepository, error) {
	// ensure install directory exists
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		return nil, fmt.Errorf("failed to create install directory: %w", err)
	}

	// merge all roots: preloaded directories + install directory
	allRoots := make([]string, 0, len(preloadedDirs)+1)
	allRoots = append(allRoots, preloadedDirs...)
	allRoots = append(allRoots, installDir)

	fsRepo, err := NewFSRepository(allRoots...)
	if err != nil {
		return nil, err
	}

	return &UserInstallableRepository{
		FSRepository: fsRepo,
		installDir:   installDir,
	}, nil
}

// InstallDir returns the directory where skills are installed
func (r *UserInstallableRepository) InstallDir() string {
	return r.installDir
}

// Install installs a skill from the specified path (copies to install directory)
func (r *UserInstallableRepository) Install(name string, sourcePath string) error {
	destDir := filepath.Join(r.installDir, name)

	// check if already exists
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("skill %q already installed", name)
	}

	// copy source directory to install directory
	if err := copyDir(sourcePath, destDir); err != nil {
		// clean up failed installation
		os.RemoveAll(destDir)
		return fmt.Errorf("failed to install skill: %w", err)
	}

	skillFile := filepath.Join(destDir, SkillFileName)
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		os.RemoveAll(destDir)
		return fmt.Errorf("installed directory does not contain %s", SkillFileName)
	}

	return r.Refresh()
}

// InstallFromURL installs a skill from the specified URL
func (r *UserInstallableRepository) InstallFromURL(name string, url string) error {
	// create temporary directory
	tmpDir, err := os.MkdirTemp("", "skill-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	downloadPath := filepath.Join(tmpDir, "download")
	if err := downloadFile(url, downloadPath); err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, dirPerm); err != nil {
		return err
	}

	if err := extractArchive(downloadPath, extractDir, url); err != nil {
		return fmt.Errorf("failed to extract: %w", err)
	}

	skillDir, err := findSkillDir(extractDir)
	if err != nil {
		return fmt.Errorf("no valid skill found in archive: %w", err)
	}

	return r.Install(name, skillDir)
}

// InstallFromUpload installs a skill from the specified data
func (r *UserInstallableRepository) InstallFromUpload(name string, data []byte, filename string) error {
	// create temporary directory
	tmpDir, err := os.MkdirTemp("", "skill-upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(tmpFile, data, filePerm); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, dirPerm); err != nil {
		return err
	}

	if err := extractArchive(tmpFile, extractDir, filename); err != nil {
		return fmt.Errorf("failed to extract: %w", err)
	}

	skillDir, err := findSkillDir(extractDir)
	if err != nil {
		return fmt.Errorf("no valid skill found in upload: %w", err)
	}

	return r.Install(name, skillDir)
}

// Uninstall uninstalls a skill
func (r *UserInstallableRepository) Uninstall(name string) error {
	// check if skill exists in install directory
	r.mu.RLock()
	entry, ok := r.index[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}

	absInstallDir, _ := filepath.Abs(r.installDir)
	absSkillDir, _ := filepath.Abs(entry.Dir)
	if !strings.HasPrefix(absSkillDir, absInstallDir) {
		return fmt.Errorf("skill %q is preloaded and cannot be uninstalled", name)
	}

	if err := os.RemoveAll(entry.Dir); err != nil {
		return fmt.Errorf("failed to remove skill directory: %w", err)
	}

	return r.Refresh()
}

// ExportSkill exports a skill as a zip file
func (r *UserInstallableRepository) ExportSkill(name string) ([]byte, error) {
	r.mu.RLock()
	entry, ok := r.index[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}

	return createZipFromDir(entry.Dir, name)
}

// downloadFile ...
func downloadFile(url string, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	if resp.ContentLength > maxDownloadBytes {
		return fmt.Errorf("file too large: %d bytes", resp.ContentLength)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	lr := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(f, lr)
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("file too large")
	}
	return nil
}

// extractArchive ...
func extractArchive(srcPath string, destDir string, hint string) error {
	lower := strings.ToLower(hint)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(srcPath, destDir)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGZ(srcPath, destDir)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(srcPath, destDir)
	default:
		// 尝试通过文件头检测
		return detectAndExtract(srcPath, destDir)
	}
}

func detectAndExtract(srcPath string, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var hdr [4]byte
	n, _ := io.ReadFull(f, hdr[:])
	if n >= 4 && string(hdr[:4]) == "PK\x03\x04" {
		return extractZip(srcPath, destDir)
	}
	if n >= 2 && hdr[0] == 0x1f && hdr[1] == 0x8b {
		return extractTarGZ(srcPath, destDir)
	}
	return fmt.Errorf("unsupported archive format")
}

func extractZip(srcPath string, destDir string) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			target := filepath.Join(destDir, filepath.FromSlash(f.Name))
			os.MkdirAll(target, dirPerm)
			continue
		}

		if f.UncompressedSize64 > uint64(maxExtractFileBytes) {
			return fmt.Errorf("file too large: %s", f.Name)
		}

		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
		if err != nil {
			rc.Close()
			return err
		}

		n, err := io.Copy(out, io.LimitReader(rc, maxExtractFileBytes+1))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		total += n
		if total > maxExtractTotalBytes {
			return fmt.Errorf("archive too large")
		}
	}
	return nil
}

func extractTar(srcPath string, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return extractTarReader(tar.NewReader(f), destDir)
}

func extractTarGZ(srcPath string, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractTarReader(tar.NewReader(gz), destDir)
}

func extractTarReader(tr *tar.Reader, destDir string) error {
	var total int64
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		target := filepath.Join(destDir, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, dirPerm)
		case tar.TypeReg:
			if h.Size > maxExtractFileBytes {
				return fmt.Errorf("file too large: %s", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
			if err != nil {
				return err
			}
			n, err := io.CopyN(out, tr, h.Size)
			out.Close()
			if err != nil {
				return err
			}
			total += n
			if total > maxExtractTotalBytes {
				return fmt.Errorf("archive too large")
			}
		}
	}
}

// findSkillDir find skill
func findSkillDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, SkillFileName)); err == nil {
		return dir, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(subDir, SkillFileName)); err == nil {
			return subDir, nil
		}
	}

	return "", fmt.Errorf("no %s found", SkillFileName)
}

// copyDir ...
func copyDir(src string, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile ...
func copyFile(src string, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, filePerm)
}

// createZipFromDir ...
func createZipFromDir(dir string, name string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "skill-export-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	w := zip.NewWriter(tmpFile)

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		archivePath := filepath.Join(name, rel)
		archivePath = filepath.ToSlash(archivePath)

		if info.IsDir() {
			_, err := w.Create(archivePath + "/")
			return err
		}

		fw, err := w.Create(archivePath)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		_, err = fw.Write(data)
		return err
	})

	if err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	tmpFile.Seek(0, 0)
	return io.ReadAll(tmpFile)
}
