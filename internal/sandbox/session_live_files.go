package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxSessionLiveFileBytes bounds the decoded file body. The fixed helper
	// transports bytes as base64 over the existing exec surface, so an explicit
	// cap protects both the Go process and the sandbox helper.
	MaxSessionLiveFileBytes = 16 << 20
	maxSessionLivePathBytes = 1024
	// maxSessionLiveListEntries caps one directory listing so a huge output
	// tree cannot inflate helper stdout into the API process.
	maxSessionLiveListEntries = 1024
	maxSessionLiveTreeDepth   = 64
	maxSessionLiveTreeEntries = 8192
	sessionLiveFileTimeout    = 45 * time.Second
)

var (
	// ErrLiveFileInvalidPath means a browser path is not a relative POSIX path.
	ErrLiveFileInvalidPath = errors.New("sandbox: invalid live file path")
	// ErrLiveFileNotFound means the requested output entry does not exist.
	ErrLiveFileNotFound = errors.New("sandbox: live file not found")
	// ErrLiveFileConflict means a no-overwrite upload or rename target exists.
	ErrLiveFileConflict = errors.New("sandbox: live file already exists")
	// ErrLiveFileUnsafe means a path resolves through or names an unsafe node.
	ErrLiveFileUnsafe = errors.New("sandbox: unsafe live file node")
	// ErrLiveFileTooLarge means a transfer exceeds MaxSessionLiveFileBytes.
	ErrLiveFileTooLarge = errors.New("sandbox: live file is too large")
	// ErrLiveFileTooMany means a listing or delete tree exceeds the entry/depth cap.
	ErrLiveFileTooMany = errors.New("sandbox: live file tree is too large")
)

type liveFileRequest struct {
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
	Data      string `json:"data,omitempty"`
	MaxBytes  int    `json:"max_bytes"`
}

type liveFileResponse struct {
	OK      bool                    `json:"ok"`
	Code    string                  `json:"code,omitempty"`
	Message string                  `json:"message,omitempty"`
	Data    string                  `json:"data,omitempty"`
	Entries []liveFileResponseEntry `json:"entries,omitempty"`
}

type liveFileResponseEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
}

// SessionLiveFileManager advertises the shared safe helper on filesystem-capable backends.
func (m *SessionBoundManager) SessionLiveFileManager() SessionLiveFileManager {
	if m == nil || m.remoteDisabled() || !m.client.Capabilities().SupportsFilesystemEnumeration {
		return nil
	}
	return m
}

// ListSessionLiveFiles lists one safe directory level below /workspace/output.
func (m *SessionBoundManager) ListSessionLiveFiles(
	ctx context.Context, sessionID, relativeDir string,
) ([]SessionLiveFileEntry, error) {
	clean, err := cleanSessionLivePath(relativeDir, true)
	if err != nil {
		return nil, err
	}
	response, err := m.runSessionLiveFileOperation(ctx, sessionID, liveFileRequest{
		Operation: "list",
		Path:      clean,
		MaxBytes:  MaxSessionLiveFileBytes,
	})
	if err != nil {
		return nil, err
	}
	if len(response.Entries) > maxSessionLiveListEntries {
		return nil, ErrLiveFileTooMany
	}
	entries := make([]SessionLiveFileEntry, 0, len(response.Entries))
	for _, entry := range response.Entries {
		cleanPath, err := cleanSessionLivePath(entry.Path, false)
		if err != nil || cleanPath != entry.Path || entry.Name == "" ||
			strings.Contains(entry.Name, "/") || path.Base(entry.Path) != entry.Name {
			return nil, ErrLiveFileUnsafe
		}
		entries = append(entries, SessionLiveFileEntry{
			Name:    entry.Name,
			Path:    cleanPath,
			Type:    liveFileEntryType(entry.Type),
			Size:    entry.Size,
			ModTime: time.Unix(0, entry.ModTimeNS).UTC(),
		})
	}
	return entries, nil
}

// ReadSessionLiveFile reads one single-link regular file below /workspace/output.
func (m *SessionBoundManager) ReadSessionLiveFile(
	ctx context.Context, sessionID, relativePath string,
) ([]byte, error) {
	clean, err := cleanSessionLivePath(relativePath, false)
	if err != nil {
		return nil, err
	}
	response, err := m.runSessionLiveFileOperation(ctx, sessionID, liveFileRequest{
		Operation: "read",
		Path:      clean,
		MaxBytes:  MaxSessionLiveFileBytes,
	})
	if err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, fmt.Errorf("sandbox: decode live file response: %w", err)
	}
	if len(content) > MaxSessionLiveFileBytes {
		return nil, ErrLiveFileTooLarge
	}
	return content, nil
}

// WriteSessionLiveFile publishes one new file without replacing an existing entry.
func (m *SessionBoundManager) WriteSessionLiveFile(
	ctx context.Context, sessionID, relativePath string, content []byte,
) error {
	if len(content) > MaxSessionLiveFileBytes {
		return ErrLiveFileTooLarge
	}
	clean, err := cleanSessionLivePath(relativePath, false)
	if err != nil {
		return err
	}
	_, err = m.runSessionLiveFileOperation(ctx, sessionID, liveFileRequest{
		Operation: "write",
		Path:      clean,
		Data:      base64.StdEncoding.EncodeToString(content),
		MaxBytes:  MaxSessionLiveFileBytes,
	})
	return err
}

// RenameSessionLiveFile atomically renames a safe entry without replacement.
func (m *SessionBoundManager) RenameSessionLiveFile(
	ctx context.Context, sessionID, source, target string,
) error {
	cleanSource, err := cleanSessionLivePath(source, false)
	if err != nil {
		return err
	}
	cleanTarget, err := cleanSessionLivePath(target, false)
	if err != nil {
		return err
	}
	if cleanSource == cleanTarget {
		return nil
	}
	_, err = m.runSessionLiveFileOperation(ctx, sessionID, liveFileRequest{
		Operation: "rename",
		Source:    cleanSource,
		Target:    cleanTarget,
		MaxBytes:  MaxSessionLiveFileBytes,
	})
	return err
}

// RemoveSessionLiveFile removes a safe entry or prevalidated directory tree.
func (m *SessionBoundManager) RemoveSessionLiveFile(
	ctx context.Context, sessionID, relativePath string,
) error {
	clean, err := cleanSessionLivePath(relativePath, false)
	if err != nil {
		return err
	}
	_, err = m.runSessionLiveFileOperation(ctx, sessionID, liveFileRequest{
		Operation: "remove",
		Path:      clean,
		MaxBytes:  MaxSessionLiveFileBytes,
	})
	return err
}

func (m *SessionBoundManager) runSessionLiveFileOperation(
	ctx context.Context, sessionID string, request liveFileRequest,
) (*liveFileResponse, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("sandbox: session ID is required for live files")
	}
	// Peek before Connect: Docker/E2B/Cube Connect resumes a paused instance
	// and can re-bill. Opening the Files tab is a GET and must not wake it.
	state, bound, err := m.peekBoundSandboxState(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !bound {
		return nil, ErrNoLiveSessionSandbox
	}
	if state != RemoteStateRunning {
		return nil, ErrSandboxPaused
	}
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoLiveSessionSandbox
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("sandbox: encode live file request: %w", err)
	}
	result, err := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command: "python3",
		Args:    []string{"-c", sessionLiveFileHelper},
		Stdin:   string(payload),
		WorkDir: SessionOutputRoot,
		User:    DefaultSandboxExecUser,
		Timeout: sessionLiveFileTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox: live file operation: %w", err)
	}
	if result == nil || result.Killed || result.ExitCode != 0 {
		message := "helper failed"
		if result != nil && strings.TrimSpace(result.Stderr) != "" {
			message = strings.TrimSpace(result.Stderr)
		}
		return nil, fmt.Errorf("sandbox: live file operation: %s", message)
	}
	response, err := decodeLiveFileResponse(result.Stdout)
	if err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, liveFileResponseError(response.Code, response.Message)
	}
	return response, nil
}

func cleanSessionLivePath(raw string, allowRoot bool) (string, error) {
	if !utf8.ValidString(raw) || len(raw) > maxSessionLivePathBytes || strings.ContainsRune(raw, 0) {
		return "", ErrLiveFileInvalidPath
	}
	if raw == "" {
		if allowRoot {
			return "", nil
		}
		return "", ErrLiveFileInvalidPath
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") {
		return "", ErrLiveFileInvalidPath
	}
	components := strings.Split(raw, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.TrimSpace(component) == "" {
			return "", ErrLiveFileInvalidPath
		}
		for _, r := range component {
			if r < 0x20 || r == 0x7f {
				return "", ErrLiveFileInvalidPath
			}
		}
	}
	return strings.Join(components, "/"), nil
}

func decodeLiveFileResponse(stdout string) (*liveFileResponse, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var response liveFileResponse
		if err := json.Unmarshal([]byte(line), &response); err == nil {
			return &response, nil
		}
	}
	return nil, errors.New("sandbox: live file helper returned an invalid response")
}

func liveFileResponseError(code, message string) error {
	var sentinel error
	switch code {
	case "not_found":
		sentinel = ErrLiveFileNotFound
	case "conflict":
		sentinel = ErrLiveFileConflict
	case "unsafe":
		sentinel = ErrLiveFileUnsafe
	case "too_large":
		sentinel = ErrLiveFileTooLarge
	case "too_many":
		sentinel = ErrLiveFileTooMany
	case "invalid":
		sentinel = ErrLiveFileInvalidPath
	default:
		return errors.New("sandbox: live file helper failed")
	}
	if strings.TrimSpace(message) == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, strings.TrimSpace(message))
}

func liveFileEntryType(raw string) SessionLiveFileType {
	switch raw {
	case "file":
		return SessionLiveFileTypeFile
	case "directory":
		return SessionLiveFileTypeDirectory
	default:
		return SessionLiveFileTypeOther
	}
}

const sessionLiveFileHelper = `
import base64
import ctypes
import errno
import json
import os
import secrets
import stat
import sys

ROOT = "/workspace/output"
NOFOLLOW = getattr(os, "O_NOFOLLOW", 0)
DIRECTORY = getattr(os, "O_DIRECTORY", 0)
MAX_LIST_ENTRIES = 1024
MAX_TREE_DEPTH = 64
MAX_TREE_ENTRIES = 8192

class LiveFileError(Exception):
    def __init__(self, code, message):
        self.code = code
        self.message = message

def fail(code, message):
    raise LiveFileError(code, message)

def parts(relative):
    if not isinstance(relative, str):
        fail("invalid", "path must be a string")
    if relative == "":
        return []
    result = relative.split("/")
    if any((not part) or part in (".", "..") for part in result):
        fail("invalid", "path must be relative POSIX components")
    return result

def open_root():
    if not NOFOLLOW or not DIRECTORY:
        fail("unsafe", "sandbox runtime lacks no-follow directory support")
    try:
        return os.open(ROOT, os.O_RDONLY | DIRECTORY | NOFOLLOW)
    except OSError:
        fail("unsafe", "output root is unavailable or unsafe")

def open_parent(root_fd, relative):
    components = parts(relative)
    if not components:
        fail("invalid", "operation requires a non-root path")
    current = os.dup(root_fd)
    try:
        for component in components[:-1]:
            next_fd = os.open(component, os.O_RDONLY | DIRECTORY | NOFOLLOW, dir_fd=current)
            os.close(current)
            current = next_fd
        return current, components[-1]
    except Exception:
        os.close(current)
        raise

def open_directory(root_fd, relative):
    components = parts(relative)
    current = os.dup(root_fd)
    try:
        for component in components:
            next_fd = os.open(component, os.O_RDONLY | DIRECTORY | NOFOLLOW, dir_fd=current)
            os.close(current)
            current = next_fd
        return current
    except Exception:
        os.close(current)
        raise

def entry_kind(mode):
    if stat.S_ISREG(mode):
        return "file"
    if stat.S_ISDIR(mode):
        return "directory"
    return "other"

def safe_kind(info):
    kind = entry_kind(info.st_mode)
    if kind == "file" and info.st_nlink != 1:
        return "other"
    return kind

def require_safe_entry(parent_fd, name, allow_directory=True):
    info = os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
    kind = safe_kind(info)
    if kind == "other" or (kind == "directory" and not allow_directory):
        fail("unsafe", "symlink or special node is not allowed")
    return info, kind

def list_entries(root_fd, relative):
    directory_fd = open_directory(root_fd, relative)
    try:
        result = []
        for name in sorted(os.listdir(directory_fd)):
            if len(result) >= MAX_LIST_ENTRIES:
                fail("too_many", "directory listing exceeds the live-file entry limit")
            info = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
            rel = name if not relative else relative + "/" + name
            result.append({
                "name": name,
                "path": rel,
                "type": safe_kind(info),
                "size": info.st_size,
                "mod_time_ns": info.st_mtime_ns,
            })
        return result
    finally:
        os.close(directory_fd)

def read_file(root_fd, relative, maximum):
    parent_fd, name = open_parent(root_fd, relative)
    try:
        file_fd = os.open(name, os.O_RDONLY | NOFOLLOW, dir_fd=parent_fd)
        try:
            info = os.fstat(file_fd)
            if safe_kind(info) != "file":
                fail("unsafe", "only single-link regular files can be downloaded")
            if info.st_size > maximum:
                fail("too_large", "file exceeds the live-file size limit")
            chunks = []
            total = 0
            while True:
                chunk = os.read(file_fd, min(65536, maximum + 1 - total))
                if not chunk:
                    break
                chunks.append(chunk)
                total += len(chunk)
                if total > maximum:
                    fail("too_large", "file exceeds the live-file size limit")
            return base64.b64encode(b"".join(chunks)).decode("ascii")
        finally:
            os.close(file_fd)
    finally:
        os.close(parent_fd)

def write_file(root_fd, relative, encoded, maximum):
    try:
        content = base64.b64decode(encoded, validate=True)
    except Exception:
        fail("invalid", "upload body is not valid base64")
    if len(content) > maximum:
        fail("too_large", "file exceeds the live-file size limit")
    parent_fd, name = open_parent(root_fd, relative)
    temp_name = ".weknora-upload-" + secrets.token_hex(16)
    temp_fd = None
    try:
        try:
            os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
            fail("conflict", "destination already exists")
        except FileNotFoundError:
            pass
        temp_fd = os.open(temp_name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | NOFOLLOW, 0o600, dir_fd=parent_fd)
        view = memoryview(content)
        while view:
            written = os.write(temp_fd, view)
            if written <= 0:
                fail("unsafe", "short upload write")
            view = view[written:]
        os.fsync(temp_fd)
        os.close(temp_fd)
        temp_fd = None
        try:
            os.link(temp_name, name, src_dir_fd=parent_fd, dst_dir_fd=parent_fd, follow_symlinks=False)
        except FileExistsError:
            fail("conflict", "destination already exists")
        os.unlink(temp_name, dir_fd=parent_fd)
    finally:
        if temp_fd is not None:
            os.close(temp_fd)
        try:
            os.unlink(temp_name, dir_fd=parent_fd)
        except FileNotFoundError:
            pass
        os.close(parent_fd)

def rename_noreplace(root_fd, source, target):
    source_fd, source_name = open_parent(root_fd, source)
    target_fd, target_name = open_parent(root_fd, target)
    try:
        require_safe_entry(source_fd, source_name)
        try:
            os.stat(target_name, dir_fd=target_fd, follow_symlinks=False)
            fail("conflict", "destination already exists")
        except FileNotFoundError:
            pass
        libc = ctypes.CDLL(None, use_errno=True)
        renameat2 = getattr(libc, "renameat2", None)
        if renameat2 is None:
            fail("unsafe", "runtime lacks atomic no-replace rename")
        renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        renameat2.restype = ctypes.c_int
        result = renameat2(source_fd, os.fsencode(source_name), target_fd, os.fsencode(target_name), 1)
        if result != 0:
            value = ctypes.get_errno()
            if value == errno.EEXIST:
                fail("conflict", "destination already exists")
            raise OSError(value, os.strerror(value))
    finally:
        os.close(source_fd)
        os.close(target_fd)

def validate_tree(directory_fd, depth=0, remaining=None):
    if remaining is None:
        remaining = [MAX_TREE_ENTRIES]
    if depth > MAX_TREE_DEPTH:
        fail("too_many", "directory tree exceeds the live-file depth limit")
    for name in os.listdir(directory_fd):
        remaining[0] -= 1
        if remaining[0] < 0:
            fail("too_many", "directory tree exceeds the live-file entry limit")
        info = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        kind = safe_kind(info)
        if kind == "other":
            fail("unsafe", "directory contains a symlink or special node")
        if kind == "directory":
            child_fd = os.open(name, os.O_RDONLY | DIRECTORY | NOFOLLOW, dir_fd=directory_fd)
            try:
                validate_tree(child_fd, depth + 1, remaining)
            finally:
                os.close(child_fd)

def remove_tree(parent_fd, name):
    info, kind = require_safe_entry(parent_fd, name)
    if kind == "file":
        os.unlink(name, dir_fd=parent_fd)
        return
    directory_fd = os.open(name, os.O_RDONLY | DIRECTORY | NOFOLLOW, dir_fd=parent_fd)
    try:
        for child in os.listdir(directory_fd):
            remove_tree(directory_fd, child)
    finally:
        os.close(directory_fd)
    os.rmdir(name, dir_fd=parent_fd)

def remove_entry(root_fd, relative):
    parent_fd, name = open_parent(root_fd, relative)
    try:
        info, kind = require_safe_entry(parent_fd, name)
        if kind == "directory":
            directory_fd = os.open(name, os.O_RDONLY | DIRECTORY | NOFOLLOW, dir_fd=parent_fd)
            try:
                validate_tree(directory_fd)
            finally:
                os.close(directory_fd)
        remove_tree(parent_fd, name)
    finally:
        os.close(parent_fd)

def main():
    request = json.load(sys.stdin)
    maximum = int(request.get("max_bytes", 0))
    root_fd = open_root()
    try:
        operation = request.get("operation")
        if operation == "list":
            return {"ok": True, "entries": list_entries(root_fd, request.get("path", ""))}
        if operation == "read":
            return {"ok": True, "data": read_file(root_fd, request.get("path", ""), maximum)}
        if operation == "write":
            write_file(root_fd, request.get("path", ""), request.get("data", ""), maximum)
            return {"ok": True}
        if operation == "rename":
            rename_noreplace(root_fd, request.get("source", ""), request.get("target", ""))
            return {"ok": True}
        if operation == "remove":
            remove_entry(root_fd, request.get("path", ""))
            return {"ok": True}
        fail("invalid", "unknown live-file operation")
    finally:
        os.close(root_fd)

try:
    print(json.dumps(main(), ensure_ascii=False, separators=(",", ":")))
except LiveFileError as exc:
    print(json.dumps({"ok": False, "code": exc.code, "message": exc.message}, separators=(",", ":")))
except FileNotFoundError:
    print(json.dumps({"ok": False, "code": "not_found", "message": "path not found"}, separators=(",", ":")))
except FileExistsError:
    print(json.dumps({"ok": False, "code": "conflict", "message": "destination already exists"}, separators=(",", ":")))
except (NotADirectoryError, IsADirectoryError):
    print(json.dumps({"ok": False, "code": "unsafe", "message": "path type is not allowed"}, separators=(",", ":")))
except RecursionError:
    print(json.dumps(
        {"ok": False, "code": "too_many", "message": "directory tree exceeds the live-file depth limit"},
        separators=(",", ":"),
    ))
except OSError as exc:
    code = "unsafe" if exc.errno in (errno.ELOOP, errno.ENOTDIR, errno.EXDEV) else "internal"
    print(json.dumps({"ok": False, "code": code, "message": "filesystem operation failed"}, separators=(",", ":")))
`

var _ SessionLiveFileManager = (*SessionBoundManager)(nil)
