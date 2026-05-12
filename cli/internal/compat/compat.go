package compat

import (
	"fmt"
	"strconv"
	"strings"
)

// Level is the client-server version compat level.
type Level int

const (
	OK        Level = iota // 兼容
	SoftWarn               // 服务器旧, 部分新功能不可用
	HardError              // 不兼容, 需升级 CLI 或 server
)

func (l Level) String() string {
	switch l {
	case OK:
		return "ok"
	case SoftWarn:
		return "soft_warn"
	case HardError:
		return "hard_error"
	}
	return "unknown"
}

// Compat compares client/server version. v0.x 阶段允许漂移:
//
//	同 major, client minor ≤ server minor → OK
//	同 major, client minor > server minor → SoftWarn (server 旧, 部分新功能不可用)
//	不同 major                            → HardError
//	字符串 unparseable                    → OK (容错, 不阻塞)
//	"(unknown)" / ""                       → OK (dev build / server 字段缺失)
func Compat(serverVer, cliVer string) (Level, string) {
	sMaj, sMin, ok := parseSemver(serverVer)
	if !ok {
		return OK, ""
	}
	cMaj, cMin, ok := parseSemver(cliVer)
	if !ok {
		return OK, ""
	}
	if sMaj != cMaj {
		return HardError, fmt.Sprintf("incompatible: client %s vs server %s — upgrade required", cliVer, serverVer)
	}
	if cMin > sMin {
		return SoftWarn, fmt.Sprintf("server is older (server %s, client %s); some new features may be unavailable", serverVer, cliVer)
	}
	return OK, ""
}

// parseSemver extracts (major, minor) from "X.Y.Z" or "X.Y.Z-suffix".
// Accepts the leading "v" common in `git describe` output and Tencent/WeKnora
// tag conventions ("v0.1.0"), since both server `/system/info.version` and
// the CLI's own ldflags-injected build.Version may carry it.
// Returns ok=false 当字符串无法识别 (空 / "(unknown)" / 非数字)。
func parseSemver(s string) (major, minor int, ok bool) {
	if s == "" || s == "(unknown)" {
		return 0, 0, false
	}
	// 接受 "v" 前缀:git describe + Tencent tag 都带 v
	s = strings.TrimPrefix(s, "v")
	// 去掉 prerelease/build metadata
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return maj, min, true
}
