package permission_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
)

var errAsked = errors.New("asked")

// An allow-always answer outlives the thread that gave it, and covers
// exactly the action it was given for. The second half is the security
// property: the key names one whole command line, so approving one call
// never approves a different one.
func TestPersisting_RemembersOneExactActionAcrossProcesses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := permission.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	asked := 0
	gate := permission.Persisting(permission.GateFunc(
		func(context.Context, permission.Request) (permission.Decision, error) {
			asked++

			return permission.AllowAlways, nil
		},
	), store)

	req := permission.Request{Tool: "shell", Key: "curl https://example.com/x"}

	for range 2 {
		if got, err := gate.Ask(context.Background(), req); err != nil || got != permission.Allow &&
			got != permission.AllowAlways {
			t.Fatalf("Ask = %v (%v), want an allow", got, err)
		}
	}

	if asked != 1 {
		t.Errorf("asked %d times, want 1: an approved action is not asked again", asked)
	}

	// A second store over the same directory is what a later daemon sees.
	reopened, err := permission.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	if !reopened.Allowed(req) {
		t.Error("the approval did not survive a reopen")
	}

	for _, other := range []permission.Request{
		{Tool: "shell", Key: "curl https://example.com/y"},
		{Tool: "pty", Key: "curl https://example.com/x"},
	} {
		if reopened.Allowed(other) {
			t.Errorf("%+v reads as approved, want only the exact action approved", other)
		}
	}
}

// A store that cannot be parsed is an error rather than an empty one: the
// safe direction is to re-ask, and a file quietly ignored forever is not
// something a person would find out about.
func TestOpenStore_RefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "approvals.jsonl"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := permission.OpenStore(dir); err == nil {
		t.Fatal("OpenStore accepted an unreadable file, want an error")
	}
}

// A denial is never recorded, so nothing but what the user approved is in
// the file.
func TestPersisting_RecordsNothingForADenial(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := permission.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	gate := permission.Persisting(permission.GateFunc(
		func(context.Context, permission.Request) (permission.Decision, error) {
			return permission.Deny, errAsked
		},
	), store)

	req := permission.Request{Tool: "shell", Key: "rm -rf ."}
	if _, err := gate.Ask(context.Background(), req); !errors.Is(err, errAsked) {
		t.Fatalf("Ask error = %v, want the gate's own", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "approvals.jsonl")); !os.IsNotExist(err) {
		t.Errorf("stat approvals = %v, want no file written for a denial", err)
	}
}
