package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// projectLock is an exclusive advisory lock on a project directory's
// .ogcode/server.lock. It keeps two ogcode servers from serving the same
// project at once: the server walks past a busy port to the next one, and two
// instances would otherwise open the same SQLite database and race each other's
// sessions (one deleting a session the other is running).
//
// The file records the owning pid and bound port so a refused start can point
// at the server that already holds the project. The contents are informational
// only — the OS lock is the authority — so a file left behind by a crash never
// blocks a later start.
type projectLock struct {
	f *os.File
}

// acquireProjectLock takes the project's server lock, or fails with an error
// naming the process that holds it. It is released by Close, normally via defer
// in Server.serve.
func acquireProjectLock(dir string) (*projectLock, error) {
	lockPath := filepath.Join(dir, ".ogcode", "server.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open project lock: %w", err)
	}
	if err := tryLockFile(f); err != nil {
		pid, port := readLockInfo(f)
		_ = f.Close()
		return nil, heldLockError(dir, pid, port)
	}
	lock := &projectLock{f: f}
	lock.write(0)
	return lock, nil
}

// setPort records the port the server actually bound, once the listener exists.
func (l *projectLock) setPort(port int) {
	l.write(port)
}

// Close releases the lock. The file itself is left in place: removing it would
// race a second process that already opened the same inode.
func (l *projectLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlockErr := unlockFile(l.f)
	closeErr := l.f.Close()
	l.f = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// write replaces the file's contents with this process's pid and, once known,
// the bound port. Failures are deliberately silent: the lock is already held and
// the contents only improve the refusal message a second start prints.
func (l *projectLock) write(port int) {
	if l == nil || l.f == nil {
		return
	}
	content := fmt.Sprintf("pid %d\n", os.Getpid())
	if port > 0 {
		content += fmt.Sprintf("port %d\n", port)
	}
	if err := l.f.Truncate(0); err != nil {
		return
	}
	if _, err := l.f.Seek(0, io.SeekStart); err != nil {
		return
	}
	if _, err := l.f.WriteString(content); err != nil {
		return
	}
	_ = l.f.Sync()
}

func readLockInfo(f *os.File) (pid, port int) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, 0
	}
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "pid":
			pid = n
		case "port":
			port = n
		}
	}
	return pid, port
}

func heldLockError(dir string, pid, port int) error {
	if pid > 0 && port > 0 {
		return fmt.Errorf("ogcode is already running for this project (pid %d at http://localhost:%d); stop it there before starting another server", pid, port)
	}
	if pid > 0 {
		return fmt.Errorf("ogcode is already running for this project (pid %d); stop it there before starting another server", pid)
	}
	return fmt.Errorf("ogcode is already running for this project (%s); stop it before starting another server", dir)
}
