package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoGGUFPath is returned when an Ollama modelfile carries no FROM line
// naming a file on disk.
var ErrNoGGUFPath = errors.New("runtime: modelfile has no FROM path")

// ErrNotGGUF is returned when a resolved path is not a GGUF file.
var ErrNotGGUF = errors.New("runtime: not a GGUF file")

// ggufMagic opens every GGUF file.
const ggufMagic = "GGUF"

// GGUFResolver maps an Ollama model name to a GGUF file on disk.
type GGUFResolver func(ctx context.Context, model string) (string, error)

// ResolveGGUF returns the GGUF file backing an Ollama model name, e.g.
// "qwen3:8b". Ollama stores models as content-addressed blobs, so the path
// comes from `ollama show --modelfile`, and the file is checked to exist
// and to carry GGUF's magic before it is handed to llama-server.
func ResolveGGUF(ctx context.Context, model string) (string, error) {
	//nolint:gosec // model is this project's configured model name, not attacker input
	out, err := exec.CommandContext(ctx, "ollama", "show", "--modelfile", model).Output()
	if err != nil {
		return "", fmt.Errorf("running ollama show --modelfile %s: %w%s", model, err, stderrOf(err))
	}

	path, err := ParseModelfileGGUF(out)
	if err != nil {
		return "", fmt.Errorf("reading modelfile for %s: %w", model, err)
	}

	if err := VerifyGGUF(path); err != nil {
		return "", fmt.Errorf("model %s: %w", model, err)
	}

	return path, nil
}

// stderrOf renders a failed command's stderr, which carries Ollama's own
// diagnosis ("model 'x' not found") where the exit status carries none.
func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || len(exitErr.Stderr) == 0 {
		return ""
	}

	return ": " + strings.TrimSpace(string(exitErr.Stderr))
}

// ParseModelfileGGUF returns the path from an `ollama show --modelfile`
// capture's first FROM line. Ollama's own header repeats the model name as
// a commented FROM line, and a hand-written Modelfile may name a base model
// rather than a file, so only an uncommented FROM with an absolute path
// counts.
func ParseModelfileGGUF(out []byte) (string, error) {
	for line := range strings.Lines(string(out)) {
		trimmed := strings.TrimSpace(line)

		rest, ok := strings.CutPrefix(trimmed, "FROM ")
		if !ok {
			continue
		}

		if path := strings.TrimSpace(rest); filepath.IsAbs(path) {
			return path, nil
		}
	}

	return "", ErrNoGGUFPath
}

// VerifyGGUF reports whether path is a readable file starting with GGUF's
// magic bytes.
func VerifyGGUF(path string) error {
	//nolint:gosec // reading the model file is this function's purpose
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening model file: %w", err)
	}
	//nolint:errcheck // read-only handle, close error carries no actionable information
	defer f.Close()

	magic := make([]byte, len(ggufMagic))
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if !bytes.Equal(magic, []byte(ggufMagic)) {
		return fmt.Errorf("%w: %s", ErrNotGGUF, path)
	}

	return nil
}
