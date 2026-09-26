package manifest

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseCPU reads a CPU amount as Kubernetes writes it ("500m", "0.5", "2")
// and returns it in millicores.
func ParseCPU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "m") {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("cpu %q must be millicores like 500m", s)
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("cpu %q must be cores like 0.5 or millicores like 500m", s)
	}
	return int64(f * 1000), nil
}

var memUnits = []struct {
	suffix string
	factor int64
}{
	{"Ki", 1 << 10},
	{"Mi", 1 << 20},
	{"Gi", 1 << 30},
	{"Ti", 1 << 40},
	{"K", 1e3},
	{"M", 1e6},
	{"G", 1e9},
	{"T", 1e12},
}

// ParseMemory reads a memory amount as Kubernetes writes it ("512Mi",
// "1Gi", "500M") and returns bytes.
func ParseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, u := range memUnits {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseInt(strings.TrimSuffix(s, u.suffix), 10, 64)
			if err != nil || n <= 0 {
				break
			}
			return n * u.factor, nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("memory %q must be an amount like 512Mi or 1Gi", s)
	}
	return n, nil
}
