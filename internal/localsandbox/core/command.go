package core

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// CommandVerdict is what a command's text says about deleting files.
type CommandVerdict struct {
	// Deletes is true when any segment removes files or directories.
	Deletes bool
	// Dangerous is true for a recursive delete of the filesystem root or home.
	Dangerous bool
}

// CommandSegment is one apparent command inside a shell invocation, after
// wrappers such as sudo or env are removed. Session approval remembers these
// segments, so a later invocation that contains the same delete does not ask
// again just because the surrounding commands changed.
//
// Opaque segments hide the real delete (a shell -c script, eval, or \rm).
// Their text is the whole command, and they are never remembered for the session.
type CommandSegment struct {
	Text      string
	Dangerous bool
	Opaque    bool
	// Rule is what a session approval remembers for this segment. It is
	// empty for an opaque segment, which can only be approved once.
	Rule DeleteRule
}

// DeleteRule is the shape of one delete, in the spirit of a Codex prefix
// rule: the program (git with its subcommand) and its options, without the
// operands. Options are a sorted set, so -rf, -fr and -r -f are one rule.
type DeleteRule struct {
	Program string
	Flags   []string
}

func (r DeleteRule) String() string {
	if len(r.Flags) == 0 {
		return r.Program
	}
	return r.Program + " " + strings.Join(r.Flags, " ")
}

// Covers reports whether approving r also approves o: the same program with
// no option r lacks. Approving rm -rf covers rm; approving rm does not cover
// rm -rf.
func (r DeleteRule) Covers(o DeleteRule) bool {
	if r.Program == "" || r.Program != o.Program {
		return false
	}
	for _, flag := range o.Flags {
		if !slices.Contains(r.Flags, flag) {
			return false
		}
	}
	return true
}

func deleteRule(words []string) DeleteRule {
	program, args := programName(words[0]), words[1:]
	switch program {
	case "git":
		sub, rest := gitSubcommandArgs(args)
		return DeleteRule{Program: "git " + sub, Flags: optionSet(rest)}
	case "find":
		return DeleteRule{Program: "find", Flags: findActions(args)}
	}
	return DeleteRule{Program: program, Flags: optionSet(args)}
}

// optionSet splits grouped short options (-rf is -r -f), keeps long options
// whole, stops at --, and returns the sorted set.
func optionSet(args []string) []string {
	var flags []string
	for _, a := range args {
		switch {
		case a == "--":
			return sortedUnique(flags)
		case strings.HasPrefix(a, "--"):
			flags = append(flags, a)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for _, c := range a[1:] {
				flags = append(flags, "-"+string(c))
			}
		}
	}
	return sortedUnique(flags)
}

// findActions is what makes a find command delete. Its filters (-name,
// -type) select files and are left out, like operands.
func findActions(args []string) []string {
	var actions []string
	for i, a := range args {
		switch a {
		case "-delete":
			actions = append(actions, a)
		case "-exec", "-execdir", "-ok", "-okdir":
			if i+1 < len(args) {
				switch p := programName(args[i+1]); p {
				case "rm", "rmdir", "unlink":
					actions = append(actions, a+" "+p)
				}
			}
		}
	}
	return sortedUnique(actions)
}

func sortedUnique(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	slices.Sort(items)
	return slices.Compact(items)
}

var commandSeparators = regexp.MustCompile(`&&|\|\||[;|&\n]`)

// wrapperPrograms run the next word as the real command.
var wrapperPrograms = map[string]bool{
	"sudo": true, "env": true, "command": true, "exec": true, "builtin": true,
	"nice": true, "nohup": true, "time": true, "xargs": true,
}

// wrapperValueOptions take the next word as their value, so that word is not
// the program: xargs -n 1 rm runs rm, not 1.
var wrapperValueOptions = map[string]map[string]bool{
	"xargs": {"-n": true, "-L": true, "-I": true, "-P": true, "-s": true, "-d": true, "-E": true, "-a": true},
	"sudo":  {"-u": true, "-g": true, "-C": true, "-h": true, "-p": true, "-U": true},
	"env":   {"-u": true, "-C": true, "-S": true},
	"nice":  {"-n": true},
}

// shellPrograms run their -c argument as a script.
var shellPrograms = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true,
	"mksh": true, "fish": true, "csh": true, "tcsh": true,
}

// substitutionMarkers run a nested command whose text the segment split does
// not see: $(...), `...`, <(...), >(...).
var substitutionMarkers = []string{"$(", "`", "<(", ">("}

// quotedDelete catches delete programs the segment split cannot see, including
// a path or a leading backslash: bash -c "/bin/rm -rf out", \rm -rf out.
// It may only make the verdict stricter.
var quotedDelete = regexp.MustCompile(
	"(^|[\\s'\"(`])[/\\\\]*(?:[\\w.+-]+[/\\\\]+)*\\\\*(rm|rmdir|unlink)(\\s|$)",
)

// escapedDelete is \rm / \rmdir / \unlink, which path.Base does not treat as rm.
var escapedDelete = regexp.MustCompile(`(^|[\s'\"(` + "`" + `])\\(rm|rmdir|unlink)(\s|$)`)

// Folded copies are used where the filesystem itself ignores case, so RM and
// /bin/RM are the same program as rm. Linux stays case-sensitive.
var (
	quotedDeleteFolded  = regexp.MustCompile(`(?i)` + quotedDelete.String())
	escapedDeleteFolded = regexp.MustCompile(`(?i)` + escapedDelete.String())
)

// ClassifyCommand reports whether command deletes files. It decides whether to
// ask the user first; it is not a sandbox boundary, and a script can still
// delete files without saying so on the command line.
func ClassifyCommand(command, homeDir string) CommandVerdict {
	segments := DeleteSegments(command, homeDir)
	var v CommandVerdict
	for _, segment := range segments {
		v.Deletes = true
		v.Dangerous = v.Dangerous || segment.Dangerous
	}
	return v
}

// DeleteSegments returns every apparent command in command that removes files.
// A delete the segment split cannot name (quoted, in a shell -c script, in
// eval, behind \rm, or in a command substitution) makes the whole invocation
// one opaque segment, which cannot be remembered for the session. Script text
// is classified again, so find -delete and git clean inside it still count.
func DeleteSegments(command, homeDir string) []CommandSegment {
	out := visibleDeleteSegments(command, homeDir)
	hidden := quotedOrEscapedDelete(command) || nestedScriptDeletes(command, homeDir)
	if hidden && len(out) == 0 {
		if text := wholeCommand(command); text != "" {
			out = append(out, CommandSegment{
				Text:      text,
				Dangerous: mentionsDangerousDelete(command, homeDir),
				Opaque:    true,
			})
		}
	}
	if !hidesDelete(command) || len(out) == 0 {
		return out
	}
	text := wholeCommand(command)
	if text == "" {
		return out
	}
	return []CommandSegment{{
		Text:      text,
		Dangerous: segmentDangerous(out) || mentionsDangerousDelete(command, homeDir),
		Opaque:    true,
	}}
}

func wholeCommand(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

func visibleDeleteSegments(command, homeDir string) []CommandSegment {
	var out []CommandSegment
	for _, segment := range commandSeparators.Split(command, -1) {
		words := commandWords(segment)
		deletes, dangerous := classifySegment(words, homeDir)
		if !deletes {
			continue
		}
		text := apparentCommand(words)
		if text == "" {
			continue
		}
		out = append(out, CommandSegment{
			Text:      text,
			Dangerous: dangerous,
			Rule:      deleteRule(words[programIndex(words):]),
		})
	}
	return out
}

func quotedOrEscapedDelete(command string) bool {
	return quotedDeletePattern().MatchString(command) || escapedDeletePattern().MatchString(command)
}

func quotedDeletePattern() *regexp.Regexp {
	if caseInsensitivePaths() {
		return quotedDeleteFolded
	}
	return quotedDelete
}

func escapedDeletePattern() *regexp.Regexp {
	if caseInsensitivePaths() {
		return escapedDeleteFolded
	}
	return escapedDelete
}

// programName is the command name used for classification. On a
// case-insensitive filesystem, RM, /bin/RM, and rm.exe are all rm.
func programName(word string) string {
	base := path.Base(word)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if !caseInsensitivePaths() {
		return base
	}
	base = strings.ToLower(base)
	return strings.TrimSuffix(base, ".exe")
}

// nestedScriptDeletes classifies the body of bash/sh/zsh -c and eval. The
// outer program is the shell, so the visible pass never sees find or git.
func nestedScriptDeletes(command, homeDir string) bool {
	return scriptsDelete(hiddenScripts(command), homeDir, 0)
}

func scriptsDelete(scripts []string, homeDir string, depth int) bool {
	if depth > 6 {
		return false
	}
	for _, script := range scripts {
		script = strings.TrimSpace(script)
		if script == "" {
			continue
		}
		if len(visibleDeleteSegments(script, homeDir)) > 0 || quotedOrEscapedDelete(script) {
			return true
		}
		if scriptsDelete(hiddenScripts(script), homeDir, depth+1) {
			return true
		}
	}
	return false
}

// hiddenScripts returns the command text passed to a shell -c or eval.
func hiddenScripts(command string) []string {
	var scripts []string
	for _, segment := range commandSeparators.Split(command, -1) {
		if script, ok := segmentScript(commandWords(segment)); ok && script != "" {
			scripts = append(scripts, script)
		}
	}
	return scripts
}

// segmentScript reports whether words run a script given on the command
// line (eval, or a shell with -c) and returns that script.
func segmentScript(words []string) (string, bool) {
	i := programIndex(words)
	if i >= len(words) {
		return "", false
	}
	program, args := programName(words[i]), words[i+1:]
	if program == "eval" {
		return strings.Join(args, " "), true
	}
	if !shellPrograms[program] {
		return "", false
	}
	for j, arg := range args {
		if arg == "-c" || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c")) {
			return strings.Join(args[j+1:], " "), true
		}
	}
	return "", false
}

func segmentDangerous(segments []CommandSegment) bool {
	for _, segment := range segments {
		if segment.Dangerous {
			return true
		}
	}
	return false
}

// hidesDelete reports scripts, substitutions, and escapes whose real command
// is not the segment the splitter classified.
func hidesDelete(command string) bool {
	if escapedDeletePattern().MatchString(command) {
		return true
	}
	for _, marker := range substitutionMarkers {
		if strings.Contains(command, marker) {
			return true
		}
	}
	for _, segment := range commandSeparators.Split(command, -1) {
		if _, ok := segmentScript(commandWords(segment)); ok {
			return true
		}
	}
	return false
}

func mentionsDangerousDelete(command, homeDir string) bool {
	flat := strings.NewReplacer("'", " ", `"`, " ", "`", " ", `\`, " ").Replace(command)
	words := strings.Fields(flat)
	for i := 0; i < len(words); i++ {
		if programName(words[i]) != "rm" {
			continue
		}
		if recursiveOnHome(words[i+1:], homeDir) {
			return true
		}
	}
	return false
}

// apparentCommand is the program and its arguments, without leading wrappers.
// The program is its base name, so /bin/rm and rm are the same segment.
func apparentCommand(words []string) string {
	i := programIndex(words)
	if i >= len(words) {
		return ""
	}
	words = append([]string(nil), words[i:]...)
	words[0] = programName(words[0])
	return strings.Join(words, " ")
}

func commandWords(segment string) []string {
	var words []string
	for _, w := range strings.Fields(segment) {
		if w = strings.Trim(w, `"'(){}`); w != "" {
			words = append(words, w)
		}
	}
	return words
}

func programIndex(words []string) int {
	wrapper := ""
	for i := 0; i < len(words); i++ {
		word := words[i]
		switch {
		case isAssignment(word):
		case wrapperPrograms[programName(word)]:
			wrapper = programName(word)
		case strings.HasPrefix(word, "-"):
			if wrapperValueOptions[wrapper][word] {
				i++
			}
		default:
			return i
		}
	}
	return len(words)
}

func classifySegment(words []string, homeDir string) (deletes, dangerous bool) {
	i := programIndex(words)
	if i >= len(words) {
		return false, false
	}
	program, args := programName(words[i]), words[i+1:]
	switch program {
	case "rm":
		return true, recursiveOnHome(args, homeDir)
	case "rmdir", "unlink":
		return true, false
	case "git":
		sub, _ := gitSubcommandArgs(args)
		return sub == "rm" || sub == "clean", false
	case "find":
		return len(findActions(args)) > 0, false
	}
	return false, false
}

func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	return eq > 0 && !strings.HasPrefix(word, "-")
}

// gitSubcommandArgs returns git's subcommand and the arguments after it,
// skipping global options such as -C dir.
func gitSubcommandArgs(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" || args[i] == "-c":
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			return args[i], args[i+1:]
		}
	}
	return "", nil
}

func recursiveOnHome(args []string, homeDir string) bool {
	recursive := false
	var targets []string
	for _, a := range args {
		switch {
		case a == "--recursive":
			recursive = true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.ContainsAny(a[1:], "rR") {
				recursive = true
			}
		default:
			targets = append(targets, a)
		}
	}
	if !recursive {
		return false
	}
	for _, target := range targets {
		if isRootOrHome(target, homeDir) {
			return true
		}
	}
	return false
}

func isRootOrHome(target, homeDir string) bool {
	switch strings.TrimRight(target, "/") {
	case "", "/*", "~", "~/*", "$HOME", "${HOME}", "$HOME/*":
		return true
	}
	if homeDir == "" {
		return false
	}
	home := path.Clean(homeDir)
	clean := path.Clean(target)
	return clean == home || clean == home+"/*"
}
