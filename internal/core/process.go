package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// ErrBinaryNotFound is returned when no sing-box executable can be located.
var ErrBinaryNotFound = errors.New("core: sing-box binary not found")

// Options tunes how a sing-box instance is launched.
type Options struct {
	// BinaryPath overrides binary discovery. When empty, SINGBOX_BIN, an
	// embedded copy (single-file builds), and then the PATH are consulted.
	BinaryPath string
	// StartTimeout bounds how long Start waits for the SOCKS port to accept.
	StartTimeout time.Duration
}

// Instance is a running sing-box process with a local SOCKS endpoint.
type Instance struct {
	SocksPort  int
	cmd        *exec.Cmd
	configPath string
}

// SocksAddress returns the loopback SOCKS address tests should dial through.
func (i *Instance) SocksAddress() string {
	return fmt.Sprintf("127.0.0.1:%d", i.SocksPort)
}

// Start launches sing-box for the given server, waits until its SOCKS inbound is
// ready, and returns the running instance. The caller must Close it.
func Start(ctx context.Context, srv model.Server, opts Options) (*Instance, error) {
	bin, err := ResolveBinary(opts.BinaryPath)
	if err != nil {
		return nil, err
	}
	port, err := FreePort()
	if err != nil {
		return nil, err
	}
	cfg, err := BuildConfig(srv, port)
	if err != nil {
		return nil, err
	}

	f, err := os.CreateTemp("", "singbox-*.json")
	if err != nil {
		return nil, fmt.Errorf("core: temp config: %w", err)
	}
	configPath := f.Name()
	if _, err := f.Write(cfg); err != nil {
		_ = f.Close()
		_ = os.Remove(configPath)
		return nil, fmt.Errorf("core: write config: %w", err)
	}
	_ = f.Close()

	cmd := exec.CommandContext(ctx, bin, "run", "-c", configPath)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(configPath)
		return nil, fmt.Errorf("core: start sing-box: %w", err)
	}

	inst := &Instance{SocksPort: port, cmd: cmd, configPath: configPath}

	timeout := opts.StartTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if err := waitReady(ctx, inst, timeout); err != nil {
		_ = inst.Close()
		return nil, err
	}
	return inst, nil
}

// Close terminates the process and removes its config file.
func (i *Instance) Close() error {
	var err error
	if i.cmd != nil && i.cmd.Process != nil {
		_ = i.cmd.Process.Kill()
		_, err = i.cmd.Process.Wait()
	}
	if i.configPath != "" {
		_ = os.Remove(i.configPath)
	}
	return err
}

// waitReady polls the SOCKS port until it accepts a connection, failing fast if
// the process exits early.
func waitReady(ctx context.Context, inst *Instance, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := inst.SocksAddress()
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if inst.cmd.ProcessState != nil && inst.cmd.ProcessState.Exited() {
			return fmt.Errorf("core: sing-box exited before becoming ready")
		}
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("core: sing-box not ready within %s", timeout)
}

// ResolveBinary locates the sing-box executable: explicit path, then SINGBOX_BIN,
// then the binary embedded at build time (single-file worker), then the PATH. A
// relative path (e.g. SINGBOX_BIN=./bin/sing-box) is anchored by searching upward
// from the working directory, so it resolves the same whether a caller runs from
// the repo root or from a subdirectory (notably `go test ./...`, which runs each
// package in its own directory).
func ResolveBinary(explicit string) (string, error) {
	if explicit != "" {
		return anchorRelative(explicit), nil
	}
	if env := os.Getenv("SINGBOX_BIN"); env != "" {
		return anchorRelative(env), nil
	}
	if embedded.present() {
		return extractEmbedded(embedded.data, embedded.sum)
	}
	if path, err := exec.LookPath("sing-box"); err == nil {
		return path, nil
	}
	return "", ErrBinaryNotFound
}

// anchorRelative resolves a relative path against the first ancestor directory
// (starting at the working directory) where it exists. Absolute paths and paths
// that exist as given are returned unchanged; an unresolvable relative path is
// returned as-is so the caller surfaces the original error.
func anchorRelative(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		return p
	}
	for {
		candidate := filepath.Join(dir, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p // reached the filesystem root without a match
		}
		dir = parent
	}
}

// FreePort asks the OS for an unused loopback TCP port.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("core: free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
