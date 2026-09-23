package tools

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

// A skill fetched with a shell command lands in this session's workspace only.
// WeKnora lists skills from the workspace catalog, not from the filesystem, so
// the files work for this session when the model reads them, but the skill is
// not installed: it is not listed and other sessions never see it. On a remote
// sandbox the files also go when the sandbox is rebuilt; on a Lite host
// workspace they stay on disk, still unlisted. Left unexplained, "npx skills
// add … succeeded" reads to the user as an install that then does not stick.
//
// The command is allowed to run - a one-off use is legitimate, and an up-front
// blacklist was tried for package installs and walked past by any shell
// indirection (see skill_runtime_guard.go). Recognition here only attaches the
// truth afterwards, plus the source a real install needs. Missing a command is
// therefore cheap, and the patterns stay narrow to keep false notes rare.

// skillTemporaryLoad is one recognised skill fetch.
type skillTemporaryLoad struct {
	// Tool names what fetched it, e.g. "skills", "clawhub", "git".
	Tool string
	// Source is the locator the catalog register API accepts, or "" when the
	// command does not carry enough to derive one.
	Source string
}

var shellSegmentSeparators = regexp.MustCompile(`&&|\|\||[;|\n]`)

// detectSkillTemporaryLoad reports whether command fetches a skill, and from
// where when that can be told.
func detectSkillTemporaryLoad(command string) (skillTemporaryLoad, bool) {
	for _, segment := range shellSegmentSeparators.Split(command, -1) {
		args := shellWords(segment)
		if load, ok := skillLoadFromArgs(args); ok {
			return load, true
		}
	}
	return skillTemporaryLoad{}, false
}

// commandRunners are words that run the next word as the command: package
// runners, privilege and environment wrappers. Their flags and NAME=value
// assignments are skipped along with them.
var commandRunners = map[string]bool{
	"npx": true, "bunx": true, "pnpx": true, "pnpm": true, "yarn": true, "npm": true,
	"dlx": true, "exec": true, "sudo": true, "env": true, "time": true, "command": true,
}

// commandIndex returns where the executed command sits in one segment, past
// any runner prefix, so an installer named only as an argument (echo, grep)
// is not mistaken for one that ran.
func commandIndex(args []string) int {
	for i, arg := range args {
		word := strings.ToLower(path.Base(arg))
		switch {
		case commandRunners[word], strings.HasPrefix(arg, "-"):
			continue
		case strings.Contains(arg, "=") && !strings.Contains(arg, "/"):
			continue
		default:
			return i
		}
	}
	return len(args)
}

func skillLoadFromArgs(args []string) (skillTemporaryLoad, bool) {
	if i := commandIndex(args); i < len(args) {
		word := strings.ToLower(path.Base(args[i]))
		rest := args[i+1:]
		switch {
		case (word == "skills" || strings.HasPrefix(word, "skills@")) && subcommand(rest, "add", "install"):
			return skillTemporaryLoad{Tool: "skills", Source: skillsCLISource(rest[1:])}, true
		case (word == "clawhub" || strings.HasPrefix(word, "clawhub@")) && subcommand(rest, "install", "i"):
			return skillTemporaryLoad{Tool: "clawhub", Source: clawHubSource(firstPositional(rest[1:]))}, true
		case (word == "openclaw" || word == "hermes" || word == "gemini") &&
			subcommand(rest, "skills", "skill") && subcommand(rest[1:], "install"):
			spec := firstPositional(rest[2:])
			source := urlSource(spec)
			if word == "openclaw" && source == "" {
				source = clawHubSource(spec)
			}
			return skillTemporaryLoad{Tool: word, Source: source}, true
		case word == "git" && subcommand(rest, "clone"):
			if load, ok := gitCloneSkill(rest[1:]); ok {
				return load, true
			}
		case word == "curl" || word == "wget":
			for _, arg := range rest {
				if u := urlSource(arg); u != "" && strings.EqualFold(path.Base(u), "SKILL.md") {
					return skillTemporaryLoad{Tool: word, Source: u}, true
				}
			}
		}
	}
	return skillTemporaryLoad{}, false
}

// skillsCLISource maps `skills add` arguments (the skills.sh CLI) onto a
// locator: owner/repo@skill and owner/repo --skill name are skills.sh
// listings, a bare owner/repo is the GitHub repo, and URLs pass through.
func skillsCLISource(args []string) string {
	spec := firstPositional(args)
	if spec == "" {
		return ""
	}
	if u := urlSource(spec); u != "" {
		return u
	}
	repo, skill, _ := strings.Cut(spec, "@")
	if skill == "" {
		skill = flagValue(args, "--skill", "-s")
	}
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " :") {
		return ""
	}
	if skill != "" && skill != "*" && !strings.ContainsAny(skill, "/ ") {
		return "skills-sh:" + parts[0] + "/" + parts[1] + "/" + skill
	}
	return "https://github.com/" + parts[0] + "/" + parts[1]
}

func clawHubSource(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if u := urlSource(spec); u != "" {
		return u
	}
	if strings.HasPrefix(strings.ToLower(spec), "skills-sh:") {
		return spec
	}
	spec = strings.TrimPrefix(spec, "@")
	switch strings.Count(spec, "/") {
	case 0:
		return spec
	case 1:
		return "@" + spec
	default:
		return ""
	}
}

// gitCloneSkill treats a clone as a skill fetch only when it lands in a skills
// directory (~/.claude/skills/x, .agents/skills/x, ...): cloning a repository
// is otherwise ordinary work.
func gitCloneSkill(args []string) (skillTemporaryLoad, bool) {
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if gitCloneFlagTakesValue(arg) {
				i++
			}
			continue
		}
		positional = append(positional, arg)
	}
	if len(positional) < 2 || !inSkillsDir(positional[1]) {
		return skillTemporaryLoad{}, false
	}
	source := urlSource(positional[0])
	if source != "" {
		source = strings.TrimSuffix(source, ".git")
	}
	return skillTemporaryLoad{Tool: "git", Source: source}, true
}

func gitCloneFlagTakesValue(flag string) bool {
	switch flag {
	case "-b", "--branch", "--depth", "-o", "--origin", "--reference", "-c", "--config",
		"--filter", "--separate-git-dir", "-u", "--upload-pack", "-j", "--jobs":
		return true
	}
	return false
}

func inSkillsDir(dir string) bool {
	for _, segment := range strings.Split(dir, "/") {
		if segment == "skills" {
			return true
		}
	}
	return false
}

func subcommand(args []string, names ...string) bool {
	if len(args) == 0 {
		return false
	}
	word := strings.ToLower(args[0])
	for _, name := range names {
		if word == name {
			return true
		}
	}
	return false
}

// firstPositional skips flags. A flag's value is not told apart from a
// positional argument in general; the installers above take their source
// first, before any valued flag, which is the order their docs use.
func firstPositional(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func flagValue(args []string, names ...string) string {
	for i, arg := range args {
		for _, name := range names {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
			if value, ok := strings.CutPrefix(arg, name+"="); ok {
				return value
			}
		}
	}
	return ""
}

func urlSource(arg string) string {
	u, err := url.Parse(strings.TrimSpace(arg))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

// shellWords splits one command segment on whitespace and drops the quotes
// around each word. It is not a shell parser; it only has to read the few
// installer invocations above as they are normally typed.
func shellWords(segment string) []string {
	fields := strings.Fields(segment)
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, `"'`)
		if field != "" {
			words = append(words, field)
		}
	}
	return words
}

// skillLoadName is the label a card shows for a fetched skill before its
// SKILL.md has been read: the last segment of the locator.
func skillLoadName(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
		trimmed = u.Path
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if strings.EqualFold(path.Base(trimmed), "SKILL.md") {
		trimmed = path.Dir(trimmed)
	}
	name := path.Base(strings.TrimPrefix(trimmed, "skills-sh:"))
	name = strings.TrimPrefix(name, "@")
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// skillTemporaryLoadNote is appended to the shell result so the model tells
// the user the truth about what just happened.
func skillTemporaryLoadNote(load skillTemporaryLoad, cardAvailable bool) string {
	var b strings.Builder
	b.WriteString("Note: this only loaded the skill for this conversation. It is not installed in " +
		"the workspace: it is not in the skill list and other conversations cannot use it. " +
		"Tell the user it is temporary.")
	switch {
	case cardAvailable && load.Source != "":
		b.WriteString(" To install it for every conversation, call search_skills with source=\"" +
			load.Source + "\" so a workspace admin gets an install card.")
	case cardAvailable:
		b.WriteString(" To install it for every conversation, call search_skills with the skill's " +
			"source (a URL or registry locator) so a workspace admin gets an install card.")
	case load.Source != "":
		b.WriteString(" To install it for every conversation, a workspace admin has to add it in the " +
			"skill settings (source: " + load.Source + ").")
	default:
		b.WriteString(" To install it for every conversation, a workspace admin has to add it in the " +
			"skill settings.")
	}
	return b.String()
}
