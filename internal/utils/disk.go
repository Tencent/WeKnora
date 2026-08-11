package utils

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
)

// LocalStorageBaseDir resolves LOCAL_STORAGE_BASE_DIR with the container
// default (mirrors router/files.go localStorageBaseDir). The desktop app
// sets this env at startup (cmd/desktop/main.go) to point at the app
// support directory, so reading the env here stays correct there too.
func LocalStorageBaseDir() string {
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if baseDir == "" {
		baseDir = "/data/files"
	}
	return baseDir
}

// DiskFreeBytes returns the free bytes on the volume hosting path.
//
// The path itself may not exist yet (e.g. LOCAL_STORAGE_BASE_DIR on a
// fresh install), so we walk up to the nearest existing ancestor before
// calling statfs. We deliberately do NOT MkdirAll here: a probe function
// must not mutate the filesystem it is measuring.
func DiskFreeBytes(path string) (uint64, error) {
	probe := strings.TrimSpace(path)
	if probe == "" {
		probe = "."
	}
	probe = filepath.Clean(probe)
	for {
		if info, err := os.Stat(probe); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Reached the filesystem root without finding an existing
			// directory; stat the root itself and let disk.Usage report
			// any real error.
			break
		}
		probe = parent
	}
	usage, err := disk.Usage(probe)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}

// LocalStorageFreeBytes returns the free bytes on the volume that hosts
// the local storage root. Inside a container this observes the mounted
// volume, which is exactly the capacity an operator means by "local
// available capacity".
func LocalStorageFreeBytes() (int64, error) {
	free, err := DiskFreeBytes(LocalStorageBaseDir())
	if err != nil {
		return 0, err
	}
	return int64(free), nil
}
