package core

import (
	"os"
	"strings"
)

// inheritedEnvNames is the host environment the sandbox may see. Anything
// else (AWS_*, GITHUB_TOKEN, DYLD_*, SSH_AUTH_SOCK…) stays in the parent.
var inheritedEnvNames = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {},
	"TERM": {}, "LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "LC_MESSAGES": {},
	"TZ": {},
}

// blockedExplicitEnvNames are never taken from RunRequest.Env. Skill keys
// (AWS_*, *_TOKEN) must still overlay; these are loader-injection knobs.
var blockedExplicitEnvNames = map[string]struct{}{
	"DYLD_INSERT_LIBRARIES": {}, "DYLD_LIBRARY_PATH": {}, "DYLD_FRAMEWORK_PATH": {},
	"LD_PRELOAD": {}, "LD_LIBRARY_PATH": {},
}

// FilterInheritedEnv keeps only the allowlisted names from a raw environ.
func FilterInheritedEnv(environ []string) []string {
	out := make([]string, 0, len(inheritedEnvNames))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, allowed := inheritedEnvNames[name]; allowed || strings.HasPrefix(name, "LC_") {
			out = append(out, kv)
		}
	}
	return out
}

// EnvSlice flattens env into NAME=value entries.
func EnvSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// BuildCommandEnv is the Service-layer env contract: start from a filtered
// inherit, overlay the caller's explicit vars, then prepend extra PATH
// entries (toolchain bins). A nil or empty explicit map both mean "no extra
// vars" — they must not wipe PATH.
//
// Explicit keys are the caller's responsibility: this is how skill credentials
// reach the child. Adapters must not pass os.Environ() here. Loader-injection
// names are dropped even when explicit.
func BuildCommandEnv(explicit map[string]string, extraPATH []string) map[string]string {
	out := environToMap(FilterInheritedEnv(os.Environ()))
	for k, v := range explicit {
		if k == "" {
			continue
		}
		if _, blocked := blockedExplicitEnvNames[k]; blocked {
			continue
		}
		out[k] = v
	}
	if len(extraPATH) > 0 {
		out["PATH"] = joinPATH(extraPATH, out["PATH"])
	}
	return out
}

func environToMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		out[name] = value
	}
	return out
}

func joinPATH(extra []string, existing string) string {
	seen := make(map[string]struct{}, len(extra)+8)
	parts := make([]string, 0, len(extra)+8)
	for _, p := range extra {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	for _, p := range strings.Split(existing, string(os.PathListSeparator)) {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
