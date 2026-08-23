package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tools"
)

// A page the model was pointed at by another page is the one an attacker
// controls, so a host no search in this thread returned goes through the
// permission gate. Denying it must stop the fetch rather than warn about
// it.
func TestWebFetchAsksAboutAHostNoSearchReturned(t *testing.T) {
	t.Parallel()

	asked := 0
	gate := permission.GateFunc(func(_ context.Context, req permission.Request) (permission.Decision, error) {
		asked++

		if !strings.Contains(req.Reason, "evil.example") {
			t.Errorf("Reason = %q, want it to name the host", req.Reason)
		}

		return permission.Deny, nil
	})

	_, fetch := tools.NewWeb("", "t1", gate)

	res, err := fetch.Run(context.Background(), mustJSON(t, map[string]string{"url": "https://evil.example/x"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if asked != 1 {
		t.Errorf("the gate was asked %d times, want exactly one", asked)
	}

	if !res.IsError || !strings.Contains(res.Content, "not approved") {
		t.Errorf("result = %+v, want a refusal naming the denial", res)
	}
}

// A URL that would disclose a credential is refused before the gate is
// reached: approving a fetch is not approving sending a key to somebody
// else's server.
func TestWebFetchRefusesACredentialWithoutAsking(t *testing.T) {
	t.Parallel()

	gate := permission.GateFunc(func(context.Context, permission.Request) (permission.Decision, error) {
		t.Error("the gate was asked about a URL carrying a credential")

		return permission.Allow, nil
	})

	_, fetch := tools.NewWeb("", "t1", gate)

	res, err := fetch.Run(context.Background(), mustJSON(t, map[string]string{
		"url": "https://example.com/x?token=abc123",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError || !strings.Contains(res.Content, "credential") {
		t.Errorf("result = %+v, want a refusal naming the credential", res)
	}
}
