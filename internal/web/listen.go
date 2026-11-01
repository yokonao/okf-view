package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Listen listens on a TCP address, or on a Unix socket given as
// unix:///absolute/path in the form devproxy uses for its targets.
func Listen(addr string) (net.Listener, error) {
	if !strings.HasPrefix(addr, "unix:") {
		return net.Listen("tcp", addr)
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if u.Host != "" || !filepath.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid address %q: unix address must contain only an absolute socket path", addr)
	}
	if err := removeStaleSocket(u.Path); err != nil {
		return nil, err
	}
	return net.Listen("unix", u.Path)
}

// removeStaleSocket removes a socket left behind by a process that no longer
// listens on it.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	if conn, err := net.Dial("unix", path); err == nil {
		_ = conn.Close()
		return fmt.Errorf("%s is in use", path)
	}
	return os.Remove(path)
}
