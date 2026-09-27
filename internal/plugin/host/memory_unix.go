//go:build unix && !linux

package host

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// groupMemory sums the resident memory of the processes in each of the
// given process groups, from one run of ps.
func groupMemory(groups map[int]bool) (map[int]int64, error) {
	out, err := exec.Command("ps", "-A", "-o", "pgid=,rss=").Output()
	if err != nil {
		return nil, err
	}
	sums := make(map[int]int64, len(groups))
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		pgid, err1 := strconv.Atoi(f[0])
		kb, err2 := strconv.ParseInt(f[1], 10, 64)
		if err1 == nil && err2 == nil && groups[pgid] {
			sums[pgid] += kb * 1024
		}
	}
	return sums, sc.Err()
}
