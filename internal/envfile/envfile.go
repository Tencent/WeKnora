// Package envfile loads optional dotenv configuration before the application
// container reads environment variables.
package envfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Load reads ENV_FILE when it is set, on any build. Otherwise it only acts on
// Lite builds, loading .env.lite — first beside the running executable (the
// tarball layout), then the current working directory (the repo/dev layout).
// There is deliberately no .env fallback: a standard binary built in the repo
// root must never pick up Lite defaults, and on Lite builds .env.lite is the
// single source of file defaults, so a stray .env cannot flip Lite into the
// Redis/Asynq path or enable Langfuse tracing.
//
// godotenv never overwrites a variable already present in the process
// environment, so deployment-level settings always win over files.
//
// Missing default files are normal and ignored. An explicitly requested file,
// or a present file with invalid syntax, is reported to the caller.
func Load(lite bool) error {
	if path := strings.TrimSpace(os.Getenv("ENV_FILE")); path != "" {
		if err := godotenv.Load(path); err != nil {
			return fmt.Errorf("load ENV_FILE %q: %w", path, err)
		}
		return nil
	}
	if !lite {
		return nil
	}

	for _, path := range liteDotenvPaths() {
		if err := godotenv.Load(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("load %s: %w", path, err)
		}
		return nil
	}
	return nil
}

// liteDotenvPaths returns the .env.lite candidate locations in priority
// order: beside the running executable first, then the working directory.
func liteDotenvPaths() []string {
	paths := []string{".env.lite"}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" && dir != "." {
			paths = append([]string{filepath.Join(dir, ".env.lite")}, paths...)
		}
	}
	return paths
}
