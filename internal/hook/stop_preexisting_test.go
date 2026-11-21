package hook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
)

const (
	calcSrc  = "package calc\n\n// Double doubles n.\nfunc Double(n int) int { return 2 * n }\n"
	calcTest = "package calc\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n" +
		"\tif Double(2) != 4 {\n\t\tt.Fatal(\"Double(2) is not 4\")\n\t}\n}\n"
	// A test that fails on this machine whatever the session does, as a
	// test written for another platform does.
	calcOldTest = "package calc\n\nimport \"testing\"\n\n" +
		"func TestOtherPlatform(t *testing.T) { t.Fatal(\"not this machine\") }\n"
	calcTriple = "package calc\n\n// Triple triples n.\nfunc Triple(n int) int { return 3 * n }\n"
	calcBroken = "package calc\n\n// Double doubles n.\nfunc Double(n int) int { return n }\n"
)

// moduleRepo returns a committed Go module whose one package has a passing
// test and a test that was already failing.
func moduleRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "go.mod", "module example.com/calc\n\ngo 1.24\n")
	write(t, root, "calc.go", calcSrc)
	write(t, root, "calc_test.go", calcTest)
	write(t, root, "old_test.go", calcOldTest)
	write(t, root, ".wavez/.gitignore", "*\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "init")

	return root
}

// The e2 task of the first live runs: a test that was red before the
// session held the agent for six turns until it "fixed" what it had not
// broken.
func TestHandle_Stop_ATestThatWasAlreadyFailingDoesNotHoldTheAgent(t *testing.T) {
	t.Parallel()

	root := moduleRepo(t)
	h := hook.New(hook.Gate{Runner: gate.New()})

	start(t, h, root)
	write(t, root, "triple.go", calcTriple)

	_, err := fire(t, h, edit(root, "Write", root+"/triple.go"))
	require.NoError(t, err)

	res, err := fire(t, h, stop(root, false))
	require.NoError(t, err)

	assert.Empty(t, res.Decision, "a failure the session did not cause does not block")
	assert.Empty(t, res.Reason)
	assert.Empty(t, res.Specific.AdditionalContext, "and is not handed to the agent as work")
	assert.Contains(t, res.SystemMessage, "ALREADY FAILING before this session")
	assert.Contains(t, res.SystemMessage, "example.com/calc TestOtherPlatform")

	again, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Empty(t, again.raw, "the change set passed, so the next stop runs nothing and says nothing")
}

func TestHandle_Stop_ATestTheSessionBrokeStillHoldsTheAgent(t *testing.T) {
	t.Parallel()

	root := moduleRepo(t)
	h := hook.New(hook.Gate{Runner: gate.New()})

	start(t, h, root)
	write(t, root, "calc.go", calcBroken)

	res, err := fire(t, h, stop(root, false))
	require.NoError(t, err)

	assert.Equal(t, "block", res.Decision)
	assert.Contains(t, res.Reason, "--- FAIL: TestDouble")
	assert.NotContains(t, res.Reason, "TestOtherPlatform", "the agent is handed only what it broke")
	assert.Contains(t, res.SystemMessage, "example.com/calc TestOtherPlatform", "the person hears of the rest")

	write(t, root, "calc.go", calcSrc+"\n// Fixed again.\n")

	fixed, err := fire(t, h, stop(root, true))
	require.NoError(t, err)
	assert.Empty(t, fixed.Decision)
	assert.Empty(t, fixed.Specific.AdditionalContext)
}

func TestHandle_Stop_NotesBesideABlockReachThePersonOnce(t *testing.T) {
	t.Parallel()

	root := repo(t)
	uncompared := hook.Verdict{
		Failed: true, Fingerprint: "test:a", Text: "FAIL test\n--- FAIL: TestA",
		Notes: "NOT COMPARED test: could not ask the tree the session started from: git not on PATH\n",
	}
	h := hook.New(&fakeRunner{verdicts: []hook.Verdict{uncompared}})

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	first, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Equal(t, "block", first.Decision)
	assert.NotContains(t, first.Reason, "NOT COMPARED", "the agent is not sent after the harness")
	assert.Contains(t, first.SystemMessage, "NOT COMPARED test")

	second, err := fire(t, h, stop(root, true))
	require.NoError(t, err)
	assert.Equal(t, "block", second.Decision)
	assert.Empty(t, second.SystemMessage, "the same note is not repeated every round")
}

func TestHandle_Stop_ABaselineThatCannotBeRebuiltBlocksAsBefore(t *testing.T) {
	t.Parallel()

	root := moduleRepo(t)
	// Dirt too large to keep leaves hashes of the baseline and no content.
	write(t, root, "big.bin", string(make([]byte, 9<<20)))

	h := hook.New(hook.Gate{Runner: gate.New()})

	start(t, h, root)
	write(t, root, "triple.go", calcTriple)

	res, err := fire(t, h, stop(root, false))
	require.NoError(t, err)

	assert.Equal(t, "block", res.Decision)
	assert.Contains(t, res.Reason, "TestOtherPlatform")
	assert.Empty(t, res.SystemMessage)
}
