package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const localJobSegmentBytes = 16 * 1024 * 1024

// localJobLog appends one output stream to disk with the remote rotation
// protocol: at 16 MiB keep an 8 KiB .head once, move the segment to .1
// (overwriting), accumulate .rotated, reopen fresh. O_APPEND keeps the
// truncate-and-reopen free of sparse holes (shell 稿 §9.2 wrapper 步骤 4)。
type localJobLog struct {
	path    string
	file    *os.File
	size    int64
	rotated int64
}

func newLocalJobLog(path string) (*localJobLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &localJobLog{path: path, file: f}, nil
}

func (l *localJobLog) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		if l.size >= localJobSegmentBytes {
			if err := l.rotate(); err != nil {
				return total, err
			}
		}
		room := int64(localJobSegmentBytes) - l.size
		n, err := l.file.Write(p[:min(int64(len(p)), room)])
		l.size += int64(n)
		total += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (l *localJobLog) rotate() error {
	_, statErr := os.Stat(l.path + ".head")
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if os.IsNotExist(statErr) {
		in, err := os.Open(l.path)
		if err != nil {
			return err
		}
		head := make([]byte, min(l.size, int64(8192)))
		_, rerr := io.ReadFull(in, head)
		_ = in.Close()
		if rerr != nil {
			return rerr
		}
		if err := os.WriteFile(l.path+".head", head, 0o600); err != nil {
			return err
		}
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}
	l.rotated += l.size
	if err := writeLocalRotated(l.path+".rotated", l.rotated); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.file, l.size = f, 0
	return nil
}

func writeLocalRotated(path string, value int64) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rotated-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := fmt.Fprint(f, strconv.FormatInt(value, 10)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (l *localJobLog) Close() error { return l.file.Close() }
