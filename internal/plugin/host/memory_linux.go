//go:build linux

package host

import (
	"bytes"
	"os"
	"strconv"
)

// groupMemory sums the resident memory of the processes in each of the
// given process groups, read from /proc.
func groupMemory(groups map[int]bool) (map[int]int64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	page := int64(os.Getpagesize())
	out := make(map[int]int64, len(groups))
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // exited meanwhile
		}
		pgrp, rss, ok := parseStat(stat)
		if ok && groups[pgrp] {
			out[pgrp] += rss * page
		}
	}
	return out, nil
}

// parseStat reads the process group and resident pages from a
// /proc/<pid>/stat line. The command name may hold spaces and parentheses,
// so fields are counted after its last ')'.
func parseStat(stat []byte) (pgrp int, rssPages int64, ok bool) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, false
	}
	// After the name: state ppid pgrp ... with rss the 22nd of them.
	f := bytes.Fields(stat[i+1:])
	if len(f) < 22 {
		return 0, 0, false
	}
	pgrp, err1 := strconv.Atoi(string(f[2]))
	rss, err2 := strconv.ParseInt(string(f[21]), 10, 64)
	return pgrp, rss, err1 == nil && err2 == nil
}
