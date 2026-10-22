// Package sandbox reads and writes the shell-job directory protocol.
package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ShellJobsRoot is where job directories live inside a remote sandbox, outside
// the /workspace git worktree so checkpoints never see them (shell 稿 §9.1).
const ShellJobsRoot = "/var/lib/weknora/jobs"

const (
	shellJobSegmentBytes = 16 * 1024 * 1024
	shellJobHeadBytes    = 8 * 1024
	// Model-facing caps (shell 稿 §9.3). A kept window is 25% head and 75% tail.
	shellJobStdoutBudget = 16 * 1024
	shellJobStderrBudget = 8 * 1024
)

// ShellJobDirExistsExit is the submit script's exit when the job directory
// is already there. The caller draws another id and tries again.
const ShellJobDirExistsExit = 17

var shellJobIDRE = regexp.MustCompile(`^sj_[0-9a-f]{4}$`)

// ValidShellJobID reports whether id is a well-formed shell job handle.
func ValidShellJobID(id string) bool { return shellJobIDRE.MatchString(id) }

// NewShellJobID draws sj_ plus 4 lowercase hex digits (2 random bytes).
func NewShellJobID() (string, error) {
	var token [2]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return "sj_" + hex.EncodeToString(token[:]), nil
}

// ShellJobMeta is the persisted identity of one shell job.
type ShellJobMeta struct {
	ID              string            `json:"work_id"`
	TenantID        uint64            `json:"tenant_id"`
	SandboxTenantID uint64            `json:"sandbox_tenant_id"` // config owner, NOT the binding-key owner
	SessionID       string            `json:"session_id"`
	Root            string            `json:"root"`
	Command         string            `json:"command"` // masked; credentials never persist
	WorkDir         string            `json:"work_dir"`
	SandboxID       string            `json:"sandbox_id"`
	CreatedAt       time.Time         `json:"created_at"`
	YieldUntil      time.Time         `json:"yield_until"`
	ParentMessageID string            `json:"parent_message_id"`
	ToolCallID      string            `json:"tool_call_id"`
	Title           string            `json:"title"`
	OutputDir       string            `json:"output_dir,omitempty"`
	OutputSeen      bool              `json:"output_seen,omitempty"`
	OutputBefore    []OutputFileStamp `json:"output_before,omitempty"`
}

// OutputFileStamp is one file in the output directory before a job starts.
// A later listing is compared with it to find deliverables.
type OutputFileStamp struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ModUnixNano int64  `json:"mod_unix_nano"`
}

// StampsFromEntries keeps file entries only, capped so the job meta stays small.
// ok is false when the listing is too large to compare reliably.
func StampsFromEntries(entries []RemoteDirEntry) (stamps []OutputFileStamp, ok bool) {
	const maxOutputStamps = 200
	files := 0
	for _, entry := range entries {
		if entry.Type == RemoteEntryFile {
			files++
		}
	}
	if files > maxOutputStamps {
		return nil, false
	}
	stamps = make([]OutputFileStamp, 0, files)
	for _, entry := range entries {
		if entry.Type != RemoteEntryFile {
			continue
		}
		stamps = append(stamps, OutputFileStamp{
			Path: entry.Path, Size: entry.Size, ModUnixNano: entry.ModTime.UnixNano(),
		})
	}
	return stamps, true
}

// EntriesFromStamps restores stamps for the same comparison shell_exec uses.
func EntriesFromStamps(stamps []OutputFileStamp) []RemoteDirEntry {
	out := make([]RemoteDirEntry, 0, len(stamps))
	for _, stamp := range stamps {
		out = append(out, RemoteDirEntry{
			Path: stamp.Path, Type: RemoteEntryFile, Size: stamp.Size,
			ModTime: time.Unix(0, stamp.ModUnixNano),
		})
	}
	return out
}

// ShellJobStatus is the watcher's latest state for one shell job.
type ShellJobStatus struct {
	State         string     `json:"state"`
	WatcherPID    int        `json:"watcher_pid"`
	PGID          int        `json:"pgid"`
	TouchPID      int        `json:"touch_pid,omitempty"`
	ProcStartedAt int64      `json:"proc_started_at,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}

// ShellJobOffsets is an absolute byte cursor into the two output streams.
type ShellJobOffsets struct {
	Stdout int64 `json:"stdout"`
	Stderr int64 `json:"stderr"`
}

// ShellJobStream is one bounded view of a job output stream.
type ShellJobStream struct {
	Text         string `json:"text"`
	FromBytes    int64  `json:"from_bytes"`
	TotalBytes   int64  `json:"total_bytes"`
	OmittedBytes int64  `json:"omitted_bytes"`
	Truncated    bool   `json:"truncated"`
	// NextBytes is the absolute end of the bytes consumed by this view, before
	// UTF-8 sanitization. Nil is reserved for older snapshot producers.
	NextBytes *int64 `json:"next_bytes,omitempty"`
}

// ShellJobSnapshot is a point-in-time read of one job directory.
type ShellJobSnapshot struct {
	Meta         ShellJobMeta   `json:"meta"`
	Status       ShellJobStatus `json:"status"`
	Stdout       ShellJobStream `json:"stdout"`
	Stderr       ShellJobStream `json:"stderr"`
	WatcherAlive bool           `json:"watcher_alive"`
	Missing      bool           `json:"missing"`
	Preview      string         `json:"preview,omitempty"`
}

// ShellJobRead selects a job directory and how much of each stream to return.
type ShellJobRead struct {
	ID           string           `json:"id"`
	From         *ShellJobOffsets `json:"from,omitempty"`
	OutputBudget int              `json:"output_budget"`
	Preview      bool             `json:"preview"`
}

// WriteShellJobJSON writes v atomically (tmp + rename). The remote wrapper does
// the same dance in sh; Lite uses this helper.
func WriteShellJobJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// ReadShellJob reads one job directory with bounded IO. A nil cursor is the
// first read of a run, or an out-of-run "查看输出" read: head plus tail, with
// the tail padded from the end of .1 when the current segment is short
// (shell 稿 §9.1, §6.3). A cursor returns only bytes after that absolute
// offset. A cursor that falls in bytes rotation already discarded starts at
// the earliest byte still on disk, and OmittedBytes counts the gap.
func ReadShellJob(root string, req ShellJobRead) (ShellJobSnapshot, error) {
	if !ValidShellJobID(req.ID) {
		return ShellJobSnapshot{}, fmt.Errorf("sandbox: invalid shell job id %q", req.ID)
	}
	dir := filepath.Join(root, req.ID)
	snap := ShellJobSnapshot{}
	if err := readShellJobMeta(dir, &snap); err != nil {
		return snap, err
	}
	statusRaw, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return ShellJobSnapshot{Missing: true}, nil
		}
		return snap, err
	}
	if err := json.Unmarshal(statusRaw, &snap.Status); err != nil {
		return snap, fmt.Errorf("sandbox: read job status: %w", err)
	}
	snap.WatcherAlive = watcherAlive(dir, snap.Status.WatcherPID, snap.Meta.ID)
	snap.Stdout = readShellStream(dir, "stdout", req.From, "Stdout", req.OutputBudget)
	snap.Stderr = readShellStream(dir, "stderr", req.From, "Stderr", req.OutputBudget)
	return snap, nil
}

// readShellJobMeta loads meta.json when the watcher has written it. A job
// directory can be readable from status and stream files alone; only a missing
// status.json means the job itself is absent.
func readShellJobMeta(dir string, snap *ShellJobSnapshot) error {
	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := json.Unmarshal(metaRaw, &snap.Meta); err != nil {
		return fmt.Errorf("sandbox: read job meta: %w", err)
	}
	return nil
}

// watcherAlive reports whether status.json still names a watcher. The remote
// cmdline check runs inside the sandbox (the batch read script sees that
// /proc); this host-side reader only has the job directory.
func watcherAlive(dir string, pid int, jobID string) bool {
	_ = dir
	_ = jobID
	return pid > 0
}

func streamBudget(which string, budget int) int {
	if budget > 0 {
		stderr := budget / 3
		if which == "Stderr" {
			return stderr
		}
		return budget - stderr
	}
	if which == "Stderr" {
		return shellJobStderrBudget
	}
	return shellJobStdoutBudget
}

func readShellStream(dir, name string, from *ShellJobOffsets, which string, budget int) ShellJobStream {
	budget = streamBudget(which, budget)
	current := readFileOrEmpty(filepath.Join(dir, name))
	rotated := readRotated(filepath.Join(dir, name+".rotated"))
	previous := readFileOrEmpty(filepath.Join(dir, name+".1"))
	headFile := readFileOrEmpty(filepath.Join(dir, name+".head"))
	total := rotated + int64(len(current))
	out := ShellJobStream{TotalBytes: total}
	var text []byte
	if from == nil {
		text = readShellHeadTail(&out, current, previous, headFile, rotated, total, budget)
	} else {
		text = readShellIncremental(&out, current, previous, headFile, rotated, shellOffset(from, which), budget)
	}
	if out.OmittedBytes > 0 || int64(len(text)) != total-out.FromBytes {
		out.Truncated = true
	}
	out.Text = sanitizeText(text)
	// The local reader selects head and tail when over budget, so it has
	// consumed the end even if some middle bytes were deliberately omitted.
	next := total
	out.NextBytes = &next
	return out
}

// readShellHeadTail returns the head and the tail of one stream. The head is
// .head when the watcher saved it, otherwise the start of the current segment.
// The tail is the end of the current segment, padded from the end of .1 when
// that segment is shorter than the tail budget. Overlap is returned once.
func readShellHeadTail(
	out *ShellJobStream, current, previous, headFile []byte, rotated, total int64, budget int,
) []byte {
	headBudget := budget / 4
	tailBudget := budget - headBudget
	head, headStart, headFromFile := selectHead(current, headFile, rotated, headBudget)
	tail, tailStart := selectTail(previous, current, total, tailBudget)
	text, textStart, gap := joinHeadTail(head, headStart, headFromFile, tail, tailStart)
	out.FromBytes = textStart
	out.OmittedBytes = textStart + gap + (total - (textStart + int64(len(text)) + gap))
	if out.OmittedBytes < 0 {
		out.OmittedBytes = 0
	}
	return text
}

func selectHead(current, headFile []byte, rotated int64, headBudget int) ([]byte, int64, bool) {
	if headBudget <= 0 {
		return nil, 0, false
	}
	if len(headFile) > 0 {
		return copyFirst(headFile, headBudget), 0, true
	}
	return copyFirst(current, headBudget), rotated, false
}

func selectTail(previous, current []byte, total int64, tailBudget int) ([]byte, int64) {
	if tailBudget <= 0 {
		return nil, total
	}
	if len(current) >= tailBudget {
		return copyLast(current, tailBudget), total - int64(tailBudget)
	}
	need := tailBudget - len(current)
	prevSuffix := copyLast(previous, need)
	text := make([]byte, 0, len(prevSuffix)+len(current))
	text = append(text, prevSuffix...)
	text = append(text, current...)
	return text, total - int64(len(text))
}

func joinHeadTail(
	head []byte, headStart int64, headFromFile bool, tail []byte, tailStart int64,
) ([]byte, int64, int64) {
	if len(head) == 0 {
		return tail, tailStart, 0
	}
	if len(tail) == 0 {
		return head, headStart, 0
	}
	headEnd := headStart + int64(len(head))
	tailEnd := tailStart + int64(len(tail))
	// A head taken from the current segment is already inside a tail that
	// reaches back into .1. Keep that pad. A .head file overrides the bytes
	// at the start of the stream instead.
	if !headFromFile && tailStart <= headStart && tailEnd >= headEnd {
		return tail, tailStart, 0
	}
	if headStart <= tailStart && headEnd >= tailEnd {
		return head, headStart, 0
	}
	if tailStart >= headEnd {
		out := make([]byte, 0, len(head)+len(tail))
		out = append(out, head...)
		out = append(out, tail...)
		return out, headStart, tailStart - headEnd
	}
	skip := int(headEnd - tailStart)
	if skip < 0 {
		skip = 0
	}
	if skip > len(tail) {
		skip = len(tail)
	}
	out := make([]byte, 0, len(head)+len(tail)-skip)
	out = append(out, head...)
	out = append(out, tail[skip:]...)
	return out, headStart, 0
}

// readShellIncremental returns bytes at and after an absolute cursor. A cursor
// in a hole left by rotation starts at the next byte still on disk.
func readShellIncremental(
	out *ShellJobStream, current, previous, headFile []byte, rotated, offset int64, budget int,
) []byte {
	total := out.TotalBytes
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	prevStart := rotated
	if len(previous) > 0 {
		prevStart = rotated - int64(len(previous))
	}
	headEnd := int64(len(headFile))
	start := offset
	if !streamByteExists(start, headEnd, prevStart, rotated, total) && start < total {
		start = earliestByteAtOrAfter(start, headEnd, prevStart, rotated)
		out.OmittedBytes = start - offset
	}
	out.FromBytes = start
	text, gap := collectStream(start, headFile, previous, current, prevStart, rotated, headEnd, total)
	out.OmittedBytes += gap
	return capToBudget(out, text, budget)
}

func streamByteExists(offset, headEnd, prevStart, rotated, total int64) bool {
	if offset >= total {
		return true
	}
	if offset >= rotated {
		return true
	}
	if offset >= prevStart && offset < rotated && prevStart < rotated {
		return true
	}
	return offset < headEnd
}

func earliestByteAtOrAfter(offset, headEnd, prevStart, rotated int64) int64 {
	if offset < headEnd {
		return offset
	}
	if offset < prevStart {
		return prevStart
	}
	if offset < rotated {
		return offset
	}
	return offset
}

func collectStream(
	start int64, headFile, previous, current []byte, prevStart, rotated, headEnd, total int64,
) ([]byte, int64) {
	var text []byte
	var gap int64
	if len(headFile) > 0 && start < headEnd {
		text = append(text, headFile[start:]...)
		start = headEnd
	}
	if len(previous) > 0 && start < rotated {
		if start < prevStart {
			gap += prevStart - start
			start = prevStart
		}
		if start < rotated {
			text = append(text, previous[start-prevStart:]...)
			start = rotated
		}
	} else if start < rotated {
		gap += rotated - start
		start = rotated
	}
	if start < total {
		off := start - rotated
		if off < 0 {
			off = 0
		}
		if off < int64(len(current)) {
			text = append(text, current[off:]...)
		}
	}
	return text, gap
}

func capToBudget(out *ShellJobStream, text []byte, budget int) []byte {
	if len(text) <= budget {
		return text
	}
	headN := budget / 4
	tailN := budget - headN
	head := copyFirst(text, headN)
	tail := copyLast(text, tailN)
	kept := make([]byte, 0, len(head)+len(tail))
	kept = append(kept, head...)
	kept = append(kept, tail...)
	out.OmittedBytes += int64(len(text) - len(kept))
	return kept
}

func shellOffset(from *ShellJobOffsets, which string) int64 {
	if which == "Stdout" {
		return from.Stdout
	}
	return from.Stderr
}

func sanitizeText(b []byte) string {
	clean := make([]byte, 0, len(b))
	for _, r := range string(b) {
		if r == 0xFFFD {
			continue
		}
		clean = append(clean, string(r)...)
	}
	return string(clean)
}

func readFileOrEmpty(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func readRotated(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0
	}
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int64(c-'0')
	}
	return v
}

func copyFirst(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return nil
	}
	if len(b) <= n {
		n = len(b)
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}

func copyLast(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return nil
	}
	if len(b) <= n {
		return copyFirst(b, len(b))
	}
	out := make([]byte, n)
	copy(out, b[len(b)-n:])
	return out
}
