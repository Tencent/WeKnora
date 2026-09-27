package host

import "errors"

// groupMemory is not measured on Windows.
func groupMemory(map[int]bool) (map[int]int64, error) {
	return nil, errors.New("plugin memory is not measured on Windows")
}
