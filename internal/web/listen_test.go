package web

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenInvalidUnixAddress(t *testing.T) {
	for _, addr := range []string{"unix://host/x.sock", "unix:relative.sock", "unix:///x.sock?q=1"} {
		if ln, err := Listen(addr); err == nil {
			_ = ln.Close()
			t.Errorf("Listen(%q) succeeded", addr)
		}
	}
}

func TestListenUnix(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")

	// A socket left behind by a dead process is replaced.
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()

	ln, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}

	if other, err := Listen("unix://" + sock); err == nil {
		_ = other.Close()
		t.Error("listened on a socket in use")
	}

	_ = ln.Close()
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Errorf("socket not removed on close: %v", err)
	}
}

func TestListenUnixNotSocket(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ln, err := Listen("unix://" + file); err == nil {
		_ = ln.Close()
		t.Error("replaced a regular file")
	}
}
