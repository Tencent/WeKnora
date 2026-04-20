package sandbox

import (
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// OutputDirName is default output directory name
	OutputDirName = "out"

	// OutputDirEnvVar is the output directory environment variable name
	OutputDirEnvVar = "OUTPUT_DIR"

	// MaxOutputFiles is maximum number of files to collect
	MaxOutputFiles = 50

	// MaxOutputFileSize is maximum size of a single file (4MB)
	MaxOutputFileSize = 4 * 1024 * 1024

	// MaxTotalOutputSize is maximum total size of all files (64MB)
	MaxTotalOutputSize = 64 * 1024 * 1024

	// MaxInlineTextSize is maximum size of inline text content (256KB)
	MaxInlineTextSize = 256 * 1024
)

// CollectOutputFiles collect output files from output directory
// outputDir is the absolute path to the output directory
// patterns is an optional list of glob patterns, empty means collect all files
func CollectOutputFiles(outputDir string, patterns []string) ([]OutputFile, error) {
	info, err := os.Stat(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to access output dir: %w", err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	var files []OutputFile
	var totalSize int64

	err = filepath.WalkDir(outputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if len(files) >= MaxOutputFiles {
			return filepath.SkipAll
		}

		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if len(patterns) > 0 && !matchAnyPattern(relPath, patterns) {
			return nil
		}

		fileInfo, err := d.Info()
		if err != nil {
			return nil
		}

		if fileInfo.Size() > MaxOutputFileSize {
			return nil
		}

		if totalSize+fileInfo.Size() > MaxTotalOutputSize {
			return filepath.SkipAll
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		totalSize += fileInfo.Size()

		mimeType := detectMIMEType(relPath, data)
		isText := isTextMIME(mimeType)

		outputFile := OutputFile{
			Name:      relPath,
			Data:      data,
			MIMEType:  mimeType,
			SizeBytes: fileInfo.Size(),
			IsText:    isText,
		}

		if isText && len(data) <= MaxInlineTextSize {
			if utf8.Valid(data) {
				outputFile.Content = string(data)
			}
		}

		files = append(files, outputFile)
		return nil
	})

	if err != nil {
		return files, fmt.Errorf("error walking output dir: %w", err)
	}

	return files, nil
}

// matchAnyPattern checks if the given name matches any of the patterns
func matchAnyPattern(name string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		simplePattern := strings.ReplaceAll(pattern, "**", "*")
		matched, err := filepath.Match(simplePattern, name)
		if err == nil && matched {
			return true
		}
		baseName := filepath.Base(name)
		matched, err = filepath.Match(simplePattern, baseName)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// detectMIMEType checks the MIME type of the file
func detectMIMEType(filename string, data []byte) string {
	// First try to detect by extension
	ext := filepath.Ext(filename)
	if ext != "" {
		mimeType := mime.TypeByExtension(ext)
		if mimeType != "" {
			return mimeType
		}
	}

	if len(data) > 0 {
		mimeType := http.DetectContentType(data)
		return mimeType
	}

	return "application/octet-stream"
}

// isTextMIME checks if the MIME type is text
func isTextMIME(mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/typescript",
		"application/x-yaml",
		"application/yaml",
		"application/toml",
		"application/x-sh",
		"application/x-python",
		"application/sql",
		"application/graphql",
	}
	for _, t := range textTypes {
		if strings.HasPrefix(mimeType, t) {
			return true
		}
	}
	return false
}

// PrepareOutputDir creates the output directory
func PrepareOutputDir(workDir string) (string, error) {
	outputDir := filepath.Join(workDir, OutputDirName)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}
	return outputDir, nil
}

// CleanupOutputDir removes the output directory
func CleanupOutputDir(outputDir string) error {
	if outputDir == "" {
		return nil
	}
	return os.RemoveAll(outputDir)
}
