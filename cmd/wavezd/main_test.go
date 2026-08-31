package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/daemon"
)

// TestServeNoListeningClaimWhenBindFails pins the announcement order: serve
// claims the socket only after Bind succeeds, so a daemon that cannot bind
// (here: a socket path past the unix address-length limit, which fails inside
// listen before any OS bind) exits with an error and never prints the
// listening claim.
func TestServeNoListeningClaimWhenBindFails(t *testing.T) {
	t.Parallel()
	sock := "/" + strings.Repeat("s", daemon.MaxSockPath+10)

	stderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = stderr }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- serve(context.Background(), "", sock) }()

	if err := w.Close(); err != nil {
		t.Fatalf("closing stderr pipe: %v", err)
	}

	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("reading stderr: %v", readErr)
	}

	err = <-serveErr
	if err == nil {
		t.Fatal("serve succeeded with an unbindable socket path")
	}
	if !errors.Is(err, daemon.ErrSockPathTooLong) {
		t.Fatalf("serve error = %v, want ErrSockPathTooLong", err)
	}
	if strings.Contains(string(out), "wavezd listening on") {
		t.Fatalf("serve printed the listening claim despite a failed bind; stderr:\n%s", out)
	}
}
