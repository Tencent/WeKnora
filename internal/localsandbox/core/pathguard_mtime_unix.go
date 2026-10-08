//go:build unix

package core

import (
	"time"

	"golang.org/x/sys/unix"
)

func statModTime(st unix.Stat_t) time.Time {
	return time.Unix(st.Mtim.Sec, st.Mtim.Nsec)
}
