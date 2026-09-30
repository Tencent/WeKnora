// Package envfile loads optional dotenv configuration before the application
// container reads environment variables.
package envfile

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Load reads ENV_FILE when it is set. Otherwise it loads .env.lite first and
// .env second, allowing the Lite template to provide its defaults while still
// accepting the standard server configuration. godotenv never overwrites a
// variable already present in the process environment, so deployment-level
// settings always win over files.
//
// Missing default files are normal for container deployments and are ignored.
// An explicitly requested file, or a present file with invalid syntax, is
// reported to the caller.
func Load() error {
	if path := strings.TrimSpace(os.Getenv("ENV_FILE")); path != "" {
		if err := godotenv.Load(path); err != nil {
			return fmt.Errorf("load ENV_FILE %q: %w", path, err)
		}
		return nil
	}

	for _, path := range []string{".env.lite", ".env"} {
		if err := godotenv.Load(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("load %s: %w", path, err)
		}
	}
	return nil
}
