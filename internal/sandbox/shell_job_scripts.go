package sandbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const shellJobMarkerPrefix = "__WEKNORA_SJ__"

// shellJobRotateFunc keeps an 8 KiB head and rotates a stream at 16 MiB.
// Truncating with `: >` leaves no sparse hole; writers only append.
var shellJobRotateFunc = fmt.Sprintf(`r(){ F="$D/$1"; S=$(wc -c < "$F" 2>/dev/null || echo 0);
  if [ "$S" -ge %d ]; then
    [ -f "$F.head" ] || head -c %d "$F" > "$F.head";
    cp "$F" "$F.1"; R=$(cat "$F.rotated" 2>/dev/null || echo 0);
    echo $(( R + S )) > "$F.rotated.tmp" && mv "$F.rotated.tmp" "$F.rotated";
    : > "$F";
  fi; }`, shellJobSegmentBytes, shellJobHeadBytes)

// shellJobForwardLoop is the submit-side poll. It copies new bytes until the
// watcher leaves "running" or the yield deadline passes. It does not wait on
// the wrapper. Each stream may forward at most 1 MiB for the whole window:
// a poll larger than the remaining allowance forwards only that remainder,
// then the read cursor moves to the bytes just observed so the next poll
// neither rereads them nor jumps over bytes already added to the allowance.
// The 50ms tick is how soon a finished command is noticed. It is not a wait
// for the yield deadline, and it is long enough to avoid a tight poll loop.
const shellJobForwardLoop = `W=$!;
sleep 0.05;
SO=0; SE=0; BO=0; BE=0; END=$(( $(date +%s)000 + @@YIELD@@ ));
p() {
  if [ "$1" = stdout ]; then
    CUR=$SO
    BUD=$BO
  else
    CUR=$SE
    BUD=$BE
  fi
  N=$(wc -c < "$D/$1" 2>/dev/null || echo 0)
  N=$(printf '%s' "$N" | tr -d '[:space:]')
  [ -n "$N" ] || N=0
  if [ "$N" -gt "$CUR" ]; then
    DLT=$(( N - CUR ))
    ROOM=$(( 1048576 - BUD ))
    if [ "$ROOM" -gt 0 ]; then
      TAKE=$DLT
      [ "$TAKE" -gt "$ROOM" ] && TAKE=$ROOM
      if [ "$1" = stdout ]; then
        dd if="$D/$1" iflag=skip_bytes,count_bytes skip="$CUR" count="$TAKE" 2>/dev/null
      else
        dd if="$D/$1" iflag=skip_bytes,count_bytes skip="$CUR" count="$TAKE" 2>/dev/null >&2
      fi
      BUD=$(( BUD + TAKE ))
    fi
    CUR=$N
  fi
  if [ "$1" = stdout ]; then
    SO=$CUR
    BO=$BUD
  else
    SE=$CUR
    BE=$BUD
  fi
}
while :; do
  if [ -f "$D/status.json" ]; then
    p stdout
    p stderr
    grep -q '"state":"running"' "$D/status.json" || break
  fi
  [ "$(date +%s)000" -ge "$END" ] && break
  sleep 0.05
done
p stdout
p stderr
`

// shellJobAbsoluteSizes publishes rotated-away bytes plus the live segment.
// wc -c of the live file alone shrinks the reported total after rotation.
const shellJobAbsoluteSizes = `RO=$(cat "$D/stdout.rotated" 2>/dev/null || echo 0)
RE=$(cat "$D/stderr.rotated" 2>/dev/null || echo 0)
LN=$(wc -c < "$D/stdout" 2>/dev/null || echo 0)
LE=$(wc -c < "$D/stderr" 2>/dev/null || echo 0)
RO=$(printf '%s' "$RO" | tr -d '[:space:]'); [ -n "$RO" ] || RO=0
RE=$(printf '%s' "$RE" | tr -d '[:space:]'); [ -n "$RE" ] || RE=0
LN=$(printf '%s' "$LN" | tr -d '[:space:]'); [ -n "$LN" ] || LN=0
LE=$(printf '%s' "$LE" | tr -d '[:space:]'); [ -n "$LE" ] || LE=0
SO=$(( RO + LN ))
SE=$(( RE + LE ))
`

const shellJobEpiloguePrint = `printf '\n%s\n' "$M"; cat "$D/status.json"; printf '%s %s\n' "$SO" "$SE"
`

// shellJobTermTrap is installed before the command starts. $C is expanded
// when the trap runs, not when it is installed.
const shellJobTermTrap = `trap 'ST=cancelled; kill -TERM -$C 2>/dev/null' TERM;`

// shellJobTimeStamp is UTC with milliseconds. Duration is shown in ms, so a
// whole-second clock would report 0 for any command that starts and ends
// inside the same second.
const shellJobTimeStamp = `date -u +%Y-%m-%dT%H:%M:%S.%3NZ`

const shellJobRunningStatus = `START=$(` + shellJobTimeStamp + `);
echo '{"state":"running","watcher_pid":'"$$"',"pgid":'"$C"',"touch_pid":'"$TOUCH"',"started_at":"'"$START"'"}' \
  > "$D/status.json.tmp" && mv "$D/status.json.tmp" "$D/status.json";`

// shellJobRotateLoop is backgrounded. The parent must wait $C itself: on
// dash, kill -0 stays true for a zombie and only wait reaps, so a foreground
// `while kill -0; do sleep; done; wait` never reaches wait. sleep is itself
// backgrounded so the parent's TERM ends the tick instead of waiting it out.
const shellJobRotateLoop = `rot() {
  SP=
  trap '[ -n "$SP" ] && kill $SP 2>/dev/null; exit 0' TERM
  while kill -0 "$C" 2>/dev/null; do
    sleep 5 &
    SP=$!
    wait $SP
    SP=
    kill -0 "$C" 2>/dev/null || exit 0
    r stdout
    r stderr
  done
}
rot &
ROT=$!;
`

// shellJobReap waits in the parent, then rotates once more so a command that
// exits before the first 5s tick still rotates a segment past 16 MiB.
// A TERM trap can make the first wait return before dash reaps; kill -0 is
// still true for that zombie, so wait once more. The body is wait, not sleep.
const shellJobReap = `wait "$C"; X=$?;
if kill -0 "$C" 2>/dev/null; then
  wait "$C"; X=$?;
fi
kill -TERM $ROT 2>/dev/null
wait $ROT 2>/dev/null
r stdout
r stderr
`

// shellJobTerminalStatus writes the terminal state exactly once.
// A prior printf-to-/dev/null draft of this record was a build leftover.
const shellJobTerminalStatus = `if [ "$ST" = cancelled ]; then
  X=143
  STATE=cancelled
elif [ "$X" = 0 ]; then
  STATE=succeeded
else
  STATE=failed
fi
E=$(` + shellJobTimeStamp + `);
echo "{\"state\":\"$STATE\",\"watcher_pid\":$$,\"pgid\":$C,\"exit_code\":$X,\
\"started_at\":\"$START\",\"ended_at\":\"$E\"}" \
  > "$D/status.json.tmp" && mv "$D/status.json.tmp" "$D/status.json";`

// One batch-read body. $J and $D are set by the caller. A missing status
// produces an explicit Missing record, distinct from a broken response. Totals are
// .rotated plus the live segment (shellJobAbsoluteSizes).
const shellJobBatchHead = `if [ -f "$D/status.json" ]; then
  META=$(cat "$D/meta.json" 2>/dev/null || printf '%s' '{}')
  ST=$(cat "$D/status.json")
`

const shellJobBatchTail = `
  PID=$(sed -n 's/.*"watcher_pid"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$D/status.json")
  ALIVE=false
  if [ -n "$PID" ] && [ -r "/proc/$PID/cmdline" ]; then
    if tr '\0' ' ' < "/proc/$PID/cmdline" | grep -F -q -- "$J"; then
      ALIVE=true
    fi
  fi
  printf '%s' '{"meta":'
  printf '%s' "$META"
  printf '%s' ',"status":'
  printf '%s' "$ST"
  printf '%s' ',"stdout":'
  stream_json stdout "$OH" "$OT" "$SF"
  printf '%s' ',"stderr":'
  stream_json stderr "$EH" "$ET" "$EF"
  printf '%s' ',"watcher_alive":'
  printf '%s' "$ALIVE"
  printf '}\n'
else
  printf '{"meta":{"work_id":"%s"},"missing":true}\n' "$J"
fi
`

// shellJobBatchPrelude defines stream_json. A first read (empty cursor) sends
// the head slice and the tail slice. A cursor sends only the bytes after it,
// capped at the stream budget. The host assembles those slices; the script
// does not decide truncation.
const shellJobBatchPrelude = `b64() { base64 2>/dev/null | tr -d '\n'; }
ntrim() { printf '%s' "$1" | tr -d '[:space:]'; }
stream_json() {
  NAME=$1; HB=$2; TB=$3; FROM=$4
  F="$D/$NAME"; ROTF="$F.rotated"; PREVF="$F.1"; HEADF="$F.head"
  RO=0
  if [ -f "$ROTF" ]; then RO=$(ntrim "$(cat "$ROTF" 2>/dev/null || echo 0)"); [ -n "$RO" ] || RO=0; fi
  LN=0
  if [ -f "$F" ]; then LN=$(ntrim "$(wc -c < "$F" || echo 0)"); [ -n "$LN" ] || LN=0; fi
  PN=0
  if [ -f "$PREVF" ]; then PN=$(ntrim "$(wc -c < "$PREVF" || echo 0)"); [ -n "$PN" ] || PN=0; fi
  TOTAL=$(( RO + LN ))
  if [ -n "$FROM" ]; then
    BUD=$(( HB + TB ))
    PREV_START=$(( RO - PN ))
    OMITTED=0
    if [ "$FROM" -ge "$TOTAL" ]; then
      BODY=""; FROM=$TOTAL
    elif [ "$FROM" -ge "$RO" ]; then
      BODY=$(dd if="$F" iflag=skip_bytes,count_bytes skip=$(( FROM - RO )) count=$BUD 2>/dev/null | b64)
    elif [ "$PN" -gt 0 ] && [ "$FROM" -ge "$PREV_START" ]; then
      SKIP=$(( FROM - PREV_START ))
      BODY=$( { tail -c +$(( SKIP + 1 )) "$PREVF"; cat "$F"; } | head -c $BUD | b64)
    elif [ "$PN" -gt 0 ]; then
      OMITTED=$(( PREV_START - FROM )); FROM=$PREV_START
      BODY=$( { cat "$PREVF"; cat "$F"; } | head -c $BUD | b64)
    else
      OMITTED=$(( RO - FROM )); FROM=$RO
      BODY=$(head -c $BUD "$F" 2>/dev/null | b64)
    fi
    printf '%s' "{\"total_bytes\":$TOTAL,\"slices\":true,\"from\":$FROM,"
    printf '%s' "\"omitted_bytes\":$OMITTED,\"body_b64\":\"$BODY\"}"
    return
  fi
  if [ -f "$HEADF" ]; then
    HEAD_FROM=true; HEAD_START=0
    HEADB=$(head -c "$HB" "$HEADF" | b64)
  else
    HEAD_FROM=false; HEAD_START=$RO
    HEADB=$(head -c "$HB" "$F" 2>/dev/null | b64)
  fi
  if [ "$LN" -ge "$TB" ]; then
    TAILB=$(tail -c "$TB" "$F" | b64)
    TAIL_START=$(( TOTAL - TB ))
  else
    PAD=$(( TB - LN ))
    TAKE=0
    if [ -f "$PREVF" ]; then
      TAKE=$PAD; [ "$PN" -lt "$TAKE" ] && TAKE=$PN
      TAILB=$( { tail -c "$TAKE" "$PREVF"; cat "$F"; } | b64)
    else
      TAILB=$(cat "$F" 2>/dev/null | b64)
    fi
    TAIL_START=$(( TOTAL - TAKE - LN ))
  fi
  printf '%s' "{\"total_bytes\":$TOTAL,\"slices\":true,\"head_b64\":\"$HEADB\","
  printf '%s' "\"head_from_file\":$HEAD_FROM,\"head_start\":$HEAD_START,"
  printf '%s' "\"tail_b64\":\"$TAILB\",\"tail_start\":$TAIL_START}"
}
`

// ShellJobSubmitScript builds the one Exec that (1) creates the job dir and
// meta.json, (2) launches the setsid wrapper without waiting, (3) forwards new
// output bytes to its own stdout/stderr until the job ends or yieldMS elapses,
// then emits the marker line, the final status.json and the absolute offsets.
// A short command therefore costs exactly one exec round-trip.
// meta.json is the masked job. execCommand is only the wrapper argument.
func ShellJobSubmitScript(job ShellJobMeta, execCommand string, yieldMS int) (string, string) {
	return ShellJobSubmitScriptWithBackend(job, execCommand, yieldMS, false)
}

// ShellJobSubmitScriptWithBackend is ShellJobSubmitScript with the backend
// chosen by the caller. Docker is the only backend whose wrapper must touch
// the activity marker; the sandbox id is not a backend name.
func ShellJobSubmitScriptWithBackend(job ShellJobMeta, execCommand string, yieldMS int, docker bool) (string, string) {
	marker := shellJobMarkerPrefix + "_" + randomHex(6) + "__"
	meta := mustJSON(job)
	wrapper := shellJobWrapperScript(job, execCommand, docker)
	var b strings.Builder
	fmt.Fprintf(&b, "J=%q; R=%s; mkdir -p \"$R\" || exit 9; D=\"$R/$J\"; %s\n",
		job.ID, shQuote(ShellJobsRoot), shellJobCreateDir())
	fmt.Fprintf(&b, "printf '%%s' %s > \"$D/meta.json\";\n", shQuote(meta))
	fmt.Fprintf(&b, "M=%s;\n", shQuote(marker))
	fmt.Fprintf(&b, "setsid /bin/sh -c %s >/dev/null 2>&1 </dev/null &\n", shSingleQuote(wrapper))
	b.WriteString(strings.ReplaceAll(shellJobForwardLoop, "@@YIELD@@", strconv.Itoa(yieldMS)))
	b.WriteString(shellJobAbsoluteSizes)
	b.WriteString(shellJobEpiloguePrint)
	return b.String(), marker
}

// shellJobWrapperScript is the watcher: the ONLY writer of status.json. It
// starts the command in its own session (setsid), appends both streams to
// files, rotates at 16 MiB keeping an 8 KiB head, optionally touches the
// docker activity marker, traps TERM into "cancelled", and writes the terminal
// state exactly once. The parent shell waits on the command; the 5s rotator
// runs beside that wait.
func shellJobWrapperScript(job ShellJobMeta, execCommand string, docker bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "D=%s/%s; J=%s; ST=running;\n", ShellJobsRoot, job.ID, job.ID)
	b.WriteString(shellJobRotateFunc)
	b.WriteByte('\n')
	b.WriteString(shellJobTermTrap)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "setsid /bin/bash -c %s >>\"$D/stdout\" 2>>\"$D/stderr\" </dev/null &\n",
		shSingleQuote(execCommand))
	b.WriteString("C=$!;\n")
	if docker {
		b.WriteString("( while :; do sleep 30; touch /var/lib/weknora-sandbox-activity; done ) &\n")
		b.WriteString("TOUCH=$!;\n")
	} else {
		b.WriteString("TOUCH=0;\n")
	}
	b.WriteString(shellJobRunningStatus)
	b.WriteByte('\n')
	b.WriteString(shellJobRotateLoop)
	b.WriteString(shellJobReap)
	b.WriteString(shellJobTerminalStatus)
	b.WriteByte('\n')
	b.WriteString(touchKill(docker))
	b.WriteByte('\n')
	return b.String()
}

// shellJobCreateDir refuses to reuse a live or recent job directory. A
// terminal directory whose status is older than the notice retention may be
// removed so the 16-bit id space can be used again.
func shellJobCreateDir() string {
	return fmt.Sprintf(`mkdir "$D" || {
  if [ -f "$D/status.json" ] && ! grep -q '"state":"running"' "$D/status.json" &&
     [ -n "$(find "$D/status.json" -mmin +1440 2>/dev/null)" ]; then
    rm -rf "$D" && mkdir "$D" || exit %d
  else
    exit %d
  fi
};`, ShellJobDirExistsExit, ShellJobDirExistsExit)
}

// ShellJobKillOrphansScript stops a command whose watcher has died, and the
// docker activity loop that would otherwise keep the sandbox awake. Pid 1 is
// never a target. A missing process is not a failed exec.
func ShellJobKillOrphansScript(pgid, touchPID int) string {
	if pgid < 0 {
		pgid = 0
	}
	if touchPID < 0 {
		touchPID = 0
	}
	return fmt.Sprintf(`PGID=%d; TOUCH=%d;
if [ "$PGID" -gt 1 ] && [ -d "/proc/$PGID" ]; then
  kill -TERM -$PGID 2>/dev/null || true
  sleep 0.2
  kill -KILL -$PGID 2>/dev/null || true
fi
if [ "$TOUCH" -gt 1 ] && [ -r "/proc/$TOUCH/cmdline" ] &&
  tr '\0' ' ' < "/proc/$TOUCH/cmdline" | grep -F -q weknora-sandbox-activity; then
  kill -KILL "$TOUCH" 2>/dev/null || true
fi
true
`, pgid, touchPID)
}

func touchKill(docker bool) string {
	if docker {
		return "kill $TOUCH 2>/dev/null"
	}
	return ":"
}

func shSingleQuote(s string) string { return shQuote(s) }

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(buf)
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellJobBatchReadScript is the sweeper / detail-read script. For each valid
// id it cats meta.json and status.json, sends a bounded head and tail (or the
// bytes after a cursor), checks /proc/<watcher_pid>/cmdline for the job id,
// and prints one JSON object. Ids with no status.json produce a Missing record.
// sandboxID is the caller's exec target; the script runs inside that sandbox
// and does not embed it. from is nil for a first read.
func ShellJobBatchReadScript(sandboxID string, ids []string) string {
	return ShellJobBatchReadScriptFor(sandboxID, ids, nil)
}

// ShellJobBatchReadScriptFor is ShellJobBatchReadScript with a per-id cursor.
// A missing id in from is a first read. A present offset, including 0, is incremental.
func ShellJobBatchReadScriptFor(sandboxID string, ids []string, from map[string]ShellJobOffsets) string {
	return ShellJobBatchReadScriptWithBudget(sandboxID, ids, from, 0)
}

// ShellJobBatchReadScriptWithBudget bounds the combined stream bytes per job.
func ShellJobBatchReadScriptWithBudget(
	sandboxID string, ids []string, from map[string]ShellJobOffsets, budget int,
) string {
	_ = sandboxID
	var b strings.Builder
	wrote := false
	for _, id := range ids {
		if !ValidShellJobID(id) {
			continue
		}
		if !wrote {
			b.WriteString(shellJobBatchPrelude)
			stdout, stderr := streamBudget("Stdout", budget), streamBudget("Stderr", budget)
			fmt.Fprintf(&b, "OH=%d; OT=%d; EH=%d; ET=%d;\n",
				stdout/4, stdout-stdout/4, stderr/4, stderr-stderr/4)
			wrote = true
		}
		sf, ef := "", ""
		if from != nil {
			if off, ok := from[id]; ok {
				sf = strconv.FormatInt(off.Stdout, 10)
				ef = strconv.FormatInt(off.Stderr, 10)
			}
		}
		fmt.Fprintf(&b, "J=%s; D=%s; SF=%s; EF=%s;\n",
			shQuote(id), shQuote(ShellJobsRoot+"/"+id), shQuote(sf), shQuote(ef))
		b.WriteString(shellJobBatchHead)
		b.WriteString(shellJobBatchTail)
	}
	if !wrote {
		return "true\n"
	}
	return b.String()
}

// ParseShellJobBatch decodes one JSON snapshot per non-empty line.
// Lines that are not objects are skipped. Missing records are explicit; an
// absent or malformed line is a read failure for the caller. Slice fields are
// assembled with the same head/tail rules as ReadShellJob.
func ParseShellJobBatch(stdout string) []ShellJobSnapshot {
	out := make([]ShellJobSnapshot, 0)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "null" {
			continue
		}
		var raw batchLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		snap := ShellJobSnapshot{
			Meta: raw.Meta, Status: raw.Status, WatcherAlive: raw.WatcherAlive, Missing: raw.Missing,
		}
		snap.Stdout = raw.Stdout.stream()
		snap.Stderr = raw.Stderr.stream()
		out = append(out, snap)
	}
	return out
}

type batchLine struct {
	Missing      bool           `json:"missing"`
	Meta         ShellJobMeta   `json:"meta"`
	Status       ShellJobStatus `json:"status"`
	WatcherAlive bool           `json:"watcher_alive"`
	Stdout       batchStream    `json:"stdout"`
	Stderr       batchStream    `json:"stderr"`
}

type batchStream struct {
	TotalBytes   int64  `json:"total_bytes"`
	Slices       bool   `json:"slices"`
	HeadB64      string `json:"head_b64"`
	HeadFromFile bool   `json:"head_from_file"`
	HeadStart    int64  `json:"head_start"`
	TailB64      string `json:"tail_b64"`
	TailStart    int64  `json:"tail_start"`
	From         *int64 `json:"from"`
	BodyB64      string `json:"body_b64"`
	Omitted      int64  `json:"omitted_bytes"`
}

func (b batchStream) stream() ShellJobStream {
	out := ShellJobStream{TotalBytes: b.TotalBytes}
	if !b.Slices {
		return out
	}
	if b.From != nil {
		body := decodeB64(b.BodyB64)
		next := min(b.TotalBytes, *b.From+int64(len(body)))
		out.NextBytes = &next
		out.FromBytes = *b.From
		out.OmittedBytes = b.Omitted
		out.Text = sanitizeText(body)
		if out.OmittedBytes > 0 || int64(len(body)) != out.TotalBytes-out.FromBytes {
			out.Truncated = true
		}
		return out
	}
	head := decodeB64(b.HeadB64)
	next := b.TotalBytes
	out.NextBytes = &next
	tail := decodeB64(b.TailB64)
	text, textStart, gap := joinHeadTail(head, b.HeadStart, b.HeadFromFile, tail, b.TailStart)
	out.FromBytes = textStart
	out.OmittedBytes = textStart + gap + (b.TotalBytes - (textStart + int64(len(text)) + gap))
	if out.OmittedBytes < 0 {
		out.OmittedBytes = 0
	}
	if out.OmittedBytes > 0 || int64(len(text)) != b.TotalBytes-out.FromBytes {
		out.Truncated = true
	}
	out.Text = sanitizeText(text)
	return out
}

func decodeB64(s string) []byte {
	if s == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return raw
}

// ShellJobMarkerStream strips the trailing marker line and everything after it
// from forwarded output, holding back at most len(marker)-1 bytes so a marker
// split across chunks is never leaked into the preview or the result body.
type ShellJobMarkerStream struct {
	marker string
	held   []byte
	found  bool
	after  []byte
}

// NewShellJobMarkerStream splits forwarded output on marker.
func NewShellJobMarkerStream(marker string) *ShellJobMarkerStream {
	return &ShellJobMarkerStream{marker: marker}
}

// Feed returns output that is safe to forward and any bytes still held
// because they might be the start of the marker.
func (s *ShellJobMarkerStream) Feed(chunk []byte) (body, tail []byte) {
	if s.found {
		s.after = append(s.after, chunk...)
		return nil, nil
	}
	buf := append(append([]byte{}, s.held...), chunk...)
	s.held = nil
	if i := indexOf(buf, s.marker); i >= 0 {
		s.found = true
		body = buf[:i]
		s.after = append(s.after, buf[i+len(s.marker):]...)
		return body, nil
	}
	// Hold back the longest suffix that could still become the marker.
	limit := len(s.marker) - 1
	if len(buf) < limit {
		limit = len(buf)
	}
	for keep := limit; keep > 0; keep-- {
		suffix := buf[len(buf)-keep:]
		if strings.HasPrefix(s.marker, string(suffix)) {
			s.held = append([]byte{}, suffix...)
			return buf[:len(buf)-keep], s.held
		}
	}
	return buf, nil
}

// Final returns the held body when no marker arrived, or the epilogue after it.
func (s *ShellJobMarkerStream) Final() (body, epilogue string) {
	if !s.found {
		return string(s.held), ""
	}
	return "", string(s.after)
}

func indexOf(buf []byte, marker string) int {
	return bytes.Index(buf, []byte(marker))
}

// ParseShellJobEpilogue reads the status JSON and the offset line that follow
// a shell-job marker. ok is false when the epilogue is not that pair.
func ParseShellJobEpilogue(epilogue string) (ShellJobStatus, ShellJobOffsets, bool) {
	var lines []string
	for _, line := range strings.Split(epilogue, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) < 2 {
		return ShellJobStatus{}, ShellJobOffsets{}, false
	}
	var status ShellJobStatus
	statusLine := strings.TrimSpace(lines[len(lines)-2])
	if err := json.Unmarshal([]byte(statusLine), &status); err != nil {
		return ShellJobStatus{}, ShellJobOffsets{}, false
	}
	fields := strings.Fields(strings.TrimSpace(lines[len(lines)-1]))
	if len(fields) != 2 {
		return ShellJobStatus{}, ShellJobOffsets{}, false
	}
	so, err1 := strconv.ParseInt(fields[0], 10, 64)
	se, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		return ShellJobStatus{}, ShellJobOffsets{}, false
	}
	return status, ShellJobOffsets{Stdout: so, Stderr: se}, true
}

// ShellJobCancelScript rechecks ownership in the exec that signals the process. The
// directory is global on shared machines; a valid handle alone is insufficient.
func ShellJobCancelScript(meta ShellJobMeta, force bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "D=%s/%s; [ -f \"$D/status.json\" ] || exit 3;\n", ShellJobsRoot, meta.ID)
	for _, field := range []struct {
		name  string
		value any
	}{
		{"work_id", meta.ID},
		{"tenant_id", meta.TenantID},
		{"session_id", meta.SessionID},
		{"sandbox_id", meta.SandboxID},
	} {
		raw, _ := json.Marshal(field.value)
		// Meta is written atomically with json.Marshal, and each of these
		// fields is followed by a comma. This also separates tenant 7 from 70.
		pattern := fmt.Sprintf("%q:%s,", field.name, raw)
		fmt.Fprintf(&b, "grep -F -q -- %s \"$D/meta.json\" || exit 3;\n", ShellQuote(pattern))
	}
	b.WriteString(`if grep -q '"state":"running"' "$D/status.json"; then
  W=$(sed -n 's/.*"watcher_pid":\([0-9]*\).*/\1/p' "$D/status.json")
`)
	fmt.Fprintf(&b, `  if [ -n "$W" ] && grep -F -q -- %s "/proc/$W/cmdline" 2>/dev/null; then
    kill -TERM "$W" 2>/dev/null || true
    sleep 0.3
`, ShellQuote(meta.ID))
	if force {
		b.WriteString(`    if grep -q '"state":"running"' "$D/status.json"; then
      C=$(sed -n 's/.*"pgid":\([0-9]*\).*/\1/p' "$D/status.json")
      if [ -n "$C" ] && [ "$C" -gt 1 ]; then kill -KILL -"$C" 2>/dev/null || true; fi
      sleep 0.3
    fi
`)
	}
	b.WriteString(`  fi
fi
cat "$D/status.json"
`)
	return b.String()
}
