// Package localservice manages one local Camp server process per profile:
// a per-instance lock prevents competing servers or setup processes, the
// service runs as a child `camp serve` process, and profile data survives
// shutdown (nothing is deleted on stop).
package localservice

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Service supervises the local server of one instance directory.
type Service struct {
	// Dir is the instance (profile) data directory holding the lock and pid.
	Dir string
	// Command is the absolute camp binary path.
	Command string
	// ConfigPath is passed to serve via CAMP_CONFIG.
	ConfigPath string
}

func (s Service) lockPath() string { return filepath.Join(s.Dir, "server.lock") }
func (s Service) pidPath() string  { return filepath.Join(s.Dir, "server.pid") }

// Acquire takes the instance lock; release via the returned func. A second
// acquisition fails with a clear error instead of racing.
func (s Service) Acquire() (func(), error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("another camp setup or server holds %s", s.lockPath())
	}
	release := func() { f.Close(); os.Remove(s.lockPath()) }
	if _, err = f.WriteString(fmt.Sprintf("%d", os.Getpid())); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Running reports whether a live server process exists for this instance
// and returns its PID (0 when not running). A stale PID file is cleaned up.
func (s Service) Running() (int, error) {
	body, err := os.ReadFile(s.pidPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		os.Remove(s.pidPath())
		return 0, nil
	}
	if err := syscall.Kill(pid, 0); err != nil {
		os.Remove(s.pidPath()) // stale
		return 0, nil
	}
	return pid, nil
}

// Start launches the server child unless one is already running for this
// instance. The child is detached from the parent's process group so the
// server outlives setup.
func (s Service) Start(addr string) (int, error) {
	if pid, err := s.Running(); err != nil {
		return 0, err
	} else if pid != 0 {
		return pid, nil
	}
	if _, err := AcquirePort(addr); err != nil {
		return 0, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()
	attr := &os.ProcAttr{
		Dir:   s.Dir,
		Files: []*os.File{devNull, devNull, devNull},
		Env:   append(os.Environ(), "CAMP_CONFIG="+s.ConfigPath, "CAMP_LISTEN="+addr),
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	process, err := os.StartProcess(s.Command, []string{s.Command, "serve"}, attr)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(s.pidPath(), []byte(fmt.Sprintf("%d", process.Pid)), 0o600); err != nil {
		return 0, err
	}
	// Wait briefly for the listener to come up so callers do not race it.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			conn.Close()
			return process.Pid, nil
		}
		if err := syscall.Kill(process.Pid, 0); err != nil {
			return 0, fmt.Errorf("camp serve exited during startup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return process.Pid, fmt.Errorf("server did not listen on %s within 10s", addr)
}

// AcquirePort verifies the address is free to bind (a port in use fails
// the setup early with a clear message).
func AcquirePort(addr string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w", addr, err)
	}
	return listener, nil
}

// Stop terminates the server process for this instance. Profile data is
// preserved.
func (s Service) Stop() error {
	pid, err := s.Running()
	if err != nil || pid == 0 {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.Remove(s.pidPath())
	return nil
}
