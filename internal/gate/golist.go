package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// goPackage is the subset of `go list -json` output the import graph and
// the coverage adapter both need. Field names and tags match `go list
// -json`'s own wire format.
//
//nolint:tagliatelle // field names and tags match go list -json's own wire format exactly
type goPackage struct {
	ImportPath   string   `json:"ImportPath"`
	Dir          string   `json:"Dir"`
	Imports      []string `json:"Imports"`
	GoFiles      []string `json:"GoFiles"`
	TestGoFiles  []string `json:"TestGoFiles"`
	XTestGoFiles []string `json:"XTestGoFiles"`
}

// listGoPackages runs `go list -json ./...` in repoRoot.
func listGoPackages(ctx context.Context, repoRoot string) ([]goPackage, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = repoRoot

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -json in %s: %w", repoRoot, err)
	}

	dec := json.NewDecoder(bytes.NewReader(out))

	var pkgs []goPackage

	for dec.More() {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}

		pkgs = append(pkgs, p)
	}

	return pkgs, nil
}

// goModulePath runs `go list -m` in repoRoot.
func goModulePath(ctx context.Context, repoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m")
	cmd.Dir = repoRoot

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m in %s: %w", repoRoot, err)
	}

	return string(bytes.TrimSpace(out)), nil
}
