// Package seatbelt implements the sandbox backend for macOS on top of
// /usr/bin/sandbox-exec. The policy compiler in this package carries no build
// tag so it stays testable on every platform.
package seatbelt

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
)

//go:embed base.sbpl
var seatbeltBasePolicy string

// seatbeltProgram is a compiled policy ready to hand to sandbox-exec.
type seatbeltProgram struct {
	// Profile is the sbpl text. It never contains a caller-supplied path.
	Profile string
	// Params are the -DNAME=VALUE arguments carrying those paths.
	Params []string
}

// compileSeatbelt turns a core.Policy into sbpl text plus -D parameters.
//
// Every path travels as a parameter and is referenced from the profile as
// (param "NAME"). Inlining paths would require escaping them into both string
// and regex contexts, which is exactly the class of bug this avoids.
func compileSeatbelt(p core.Policy) (seatbeltProgram, error) {
	if err := p.Validate(); err != nil {
		return seatbeltProgram{}, err
	}

	var params []string
	// Four ordered tiers. sbpl gives precedence to the rule written later, so
	// the order below *is* the policy:
	//
	//	writeAllows   workspace writes, network
	//	privateDenies the user's home, denied for reading as a whole
	//	readAllows    workspace + toolchains, re-opened inside that deny
	//	denies        credentials and root anchors, final say
	//
	// Reads cannot simply sit in the first tier: the base profile grants
	// blanket file-read*, so the private deny has to come after every broad
	// allow, and the paths it must not cover have to come after it in turn.
	var writeAllows []string
	var privateDenies []string
	var readAllows []string
	var denies []string

	for i, root := range p.WritableRoots {
		rootParam := fmt.Sprintf("WRITABLE_ROOT_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", rootParam, root.Path))

		clauses := []string{fmt.Sprintf(`(subpath (param %q))`, rootParam)}
		for j, sub := range root.ReadOnlySubpaths {
			subParam := fmt.Sprintf("%s_EXCLUDED_%d", rootParam, j)
			params = append(params, fmt.Sprintf("-D%s=%s", subParam, sub))
			// Both forms are required: subpath alone still permits creating
			// the protected directory itself.
			clauses = append(clauses,
				fmt.Sprintf(`(require-not (literal (param %q)))`, subParam),
				fmt.Sprintf(`(require-not (subpath (param %q)))`, subParam),
			)
		}
		writeAllows = append(writeAllows,
			fmt.Sprintf("(allow file-write* (require-all %s))", strings.Join(clauses, " ")),
		)
		readAllows = append(readAllows,
			fmt.Sprintf("(allow file-read* (subpath (param %q)))", rootParam),
		)
		// The writable root is the authority boundary the next policy build
		// reuses; it must not be replaceable by a symlink.
		denies = append(denies, fmt.Sprintf(
			`(deny file-write-unlink (require-all (literal (param %q)) (vnode-type DIRECTORY)))`,
			rootParam))

		for j := range root.ReadOnlySubpaths {
			subParam := fmt.Sprintf("%s_EXCLUDED_%d", rootParam, j)
			// Renaming the parent of a protected subtree would move it out of
			// its carve-out, so deny unlinking the carve-out itself too.
			denies = append(denies, fmt.Sprintf(
				`(deny file-write-unlink (require-all (literal (param %q)) (vnode-type DIRECTORY)))`,
				subParam))
		}
	}

	for i, private := range p.PrivateRoots {
		param := fmt.Sprintf("PRIVATE_ROOT_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", param, private))
		// Read only. Writing is already confined to the writable roots, and
		// denying writes here would revoke the workspace along with them.
		privateDenies = append(privateDenies,
			fmt.Sprintf("(deny file-read* (subpath (param %q)))", param))
	}

	for i, root := range p.ReadableRoots {
		param := fmt.Sprintf("READABLE_ROOT_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", param, root))
		// A readable root may be a single file — a shell startup script —
		// which subpath does not match on its own.
		readAllows = append(readAllows,
			fmt.Sprintf("(allow file-read* (subpath (param %q)))", param),
			fmt.Sprintf("(allow file-read* (literal (param %q)))", param),
		)
	}

	networkRules, err := seatbeltNetworkRules(p)
	if err != nil {
		return seatbeltProgram{}, err
	}
	writeAllows = append(writeAllows, networkRules...)

	for i, deny := range p.DenyRead {
		param := fmt.Sprintf("DENY_READ_%d", i)
		params = append(params, fmt.Sprintf("-D%s=%s", param, deny))
		denies = append(denies,
			fmt.Sprintf("(deny file-read* (subpath (param %q)))", param),
			fmt.Sprintf("(deny file-read* (literal (param %q)))", param),
			fmt.Sprintf("(deny file-write* (subpath (param %q)))", param),
			fmt.Sprintf("(deny file-write* (literal (param %q)))", param),
		)
	}

	sections := []string{seatbeltBasePolicy}
	sections = append(sections, writeAllows...)
	sections = append(sections, privateDenies...)
	sections = append(sections, readAllows...)
	// Credential denies and root anchors come last so nothing above can
	// reopen them.
	sections = append(sections, denies...)

	return seatbeltProgram{
		Profile: strings.Join(sections, "\n") + "\n",
		Params:  params,
	}, nil
}

func seatbeltNetworkRules(p core.Policy) ([]string, error) {
	switch p.Network {
	case core.NetworkDenied:
		// No allow rule at all; (deny default) in the base policy covers it.
		return nil, nil
	case core.NetworkUnrestricted:
		return []string{
			"(allow network-outbound)",
			"(allow network-inbound)",
			"(allow network-bind)",
			`(allow system-socket)`,
		}, nil
	case core.NetworkLoopback:
		if len(p.LoopbackPorts) == 0 {
			// Fail closed rather than silently widening to every port.
			return nil, fmt.Errorf(
				"localsandbox: loopback network mode requires at least one port")
		}
		rules := []string{`(allow system-socket)`}
		for _, port := range p.LoopbackPorts {
			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("localsandbox: invalid loopback port %d", port)
			}
			// sandbox-exec rejects 127.0.0.1 here: "host must be * or localhost".
			rules = append(rules,
				fmt.Sprintf(`(allow network-outbound (remote ip "localhost:%d"))`, port))
		}
		return rules, nil
	default:
		return nil, fmt.Errorf("localsandbox: unknown network mode %d", p.Network)
	}
}
