package gate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kyleking/wavez/internal/codeintel"
)

const (
	manifestFilePerm     = 0o600
	truncatedOutputLimit = 300
	coverProfileFields   = 3
	coverProfilePosParts = 2
)

// CoverageWriter is the codeintel.Store method CoverageAdapter needs.
// A codeintel.Store satisfies it directly.
type CoverageWriter interface {
	WriteCoverage(ctx context.Context, testID, testHash string, rows []codeintel.CoverageRow) error
}

// CoverageAdapter runs Go's per-test coverprofile loop (DESIGN.md's Gates
// section) and writes rows into a codeintel store. It keeps its own
// manifest of which files each test last covered, by content hash,
// separate from the store's coverage rows, so a Refresh only re-runs a
// test whose covered files actually changed.
type CoverageAdapter struct {
	store        CoverageWriter
	manifestPath string
	workers      int
}

// NewCoverageAdapter builds an adapter over store, persisting its re-run
// manifest at manifestPath and running up to workers tests in parallel.
func NewCoverageAdapter(store CoverageWriter, manifestPath string, workers int) *CoverageAdapter {
	if workers < 1 {
		workers = 1
	}

	return &CoverageAdapter{store: store, manifestPath: manifestPath, workers: workers}
}

// coverageManifest is the adapter's own on-disk record of covered-file
// hashes per test.
type coverageManifest struct {
	Tests map[string]map[string]string `json:"tests"` // testID -> file -> content hash
}

// RefreshStats reports what one Refresh call did.
type RefreshStats struct {
	Considered int
	Ran        int
	Skipped    int
	Failed     int
}

// Refresh lists every Go test under repoRoot and re-runs only the ones
// whose manifest entry is missing or whose previously covered files
// changed by content hash, writing fresh coverage rows for each into the
// store.
func (a *CoverageAdapter) Refresh(ctx context.Context, repoRoot string) (RefreshStats, error) {
	man, err := loadManifest(a.manifestPath)
	if err != nil {
		return RefreshStats{}, err
	}

	modulePath, err := goModulePath(ctx, repoRoot)
	if err != nil {
		return RefreshStats{}, err
	}

	jobs, err := listGoTests(ctx, repoRoot)
	if err != nil {
		return RefreshStats{}, err
	}

	var stats RefreshStats

	stats.Considered = len(jobs)

	var toRun []goTestJob

	for _, j := range jobs {
		if testNeedsRerun(repoRoot, man, j.id()) {
			toRun = append(toRun, j)
		} else {
			stats.Skipped++
		}
	}

	results, failed := runCoverageJobs(ctx, repoRoot, modulePath, toRun, a.workers)
	stats.Ran = len(toRun)
	stats.Failed = failed

	for _, r := range results {
		hashes, err := hashCoveredFiles(repoRoot, r.rows)
		if err != nil {
			return stats, err
		}

		if err := a.store.WriteCoverage(ctx, r.id, manifestDigest(hashes), r.rows); err != nil {
			return stats, fmt.Errorf("writing coverage for %s: %w", r.id, err)
		}

		man.Tests[r.id] = hashes
	}

	if len(results) > 0 {
		if err := saveManifest(a.manifestPath, man); err != nil {
			return stats, err
		}
	}

	return stats, nil
}

func testNeedsRerun(repoRoot string, man *coverageManifest, id string) bool {
	files, ok := man.Tests[id]
	if !ok {
		return true
	}

	for file, want := range files {
		got, err := hashFile(repoRoot, file)
		if err != nil || got != want {
			return true
		}
	}

	return false
}

func hashCoveredFiles(repoRoot string, rows []codeintel.CoverageRow) (map[string]string, error) {
	files := make(map[string]struct{})
	for _, r := range rows {
		files[r.File] = struct{}{}
	}

	out := make(map[string]string, len(files))

	for f := range files {
		h, err := hashFile(repoRoot, f)
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", f, err)
		}

		out[f] = h
	}

	return out, nil
}

func hashFile(repoRoot, relPath string) (string, error) {
	//nolint:gosec // relPath comes from this project's own file list, not user input
	data, err := os.ReadFile(filepath.Join(repoRoot, relPath))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", relPath, err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

func manifestDigest(hashes map[string]string) string {
	files := make([]string, 0, len(hashes))
	for f := range hashes {
		files = append(files, f)
	}

	sort.Strings(files)

	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f))
		h.Write([]byte(hashes[f]))
	}

	return hex.EncodeToString(h.Sum(nil))
}

func loadManifest(path string) (*coverageManifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file, not user input
	if errors.Is(err, os.ErrNotExist) {
		return &coverageManifest{Tests: make(map[string]map[string]string)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading coverage manifest %s: %w", path, err)
	}

	var m coverageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing coverage manifest %s: %w", path, err)
	}

	if m.Tests == nil {
		m.Tests = make(map[string]map[string]string)
	}

	return &m, nil
}

func saveManifest(path string, m *coverageManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding coverage manifest: %w", err)
	}

	if err := os.WriteFile(path, data, manifestFilePerm); err != nil {
		return fmt.Errorf("writing coverage manifest %s: %w", path, err)
	}

	return nil
}

type goTestJob struct {
	pkg  string
	name string
}

func (j goTestJob) id() string { return j.pkg + "." + j.name }

var goTestNameRe = regexp.MustCompile(`^(Test|Example)[A-Za-z0-9_]*$`)

func listGoTests(ctx context.Context, repoRoot string) ([]goTestJob, error) {
	pkgs, err := listGoPackages(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	var jobs []goTestJob

	for i := range pkgs {
		pkg := &pkgs[i]
		if len(pkg.TestGoFiles) == 0 && len(pkg.XTestGoFiles) == 0 {
			continue
		}

		//nolint:gosec // pkg.ImportPath comes from this repo's own `go list` output, not user input
		cmd := exec.CommandContext(ctx, "go", "test", "-list", ".*", pkg.ImportPath)
		cmd.Dir = repoRoot

		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("listing tests for %s: %w", pkg.ImportPath, err)
		}

		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			name := strings.TrimSpace(sc.Text())
			if goTestNameRe.MatchString(name) {
				jobs = append(jobs, goTestJob{pkg: pkg.ImportPath, name: name})
			}
		}
	}

	return jobs, nil
}

type coverageResult struct {
	id   string
	rows []codeintel.CoverageRow
}

//nolint:gocritic // named returns here would trip nonamedreturns instead; callers read (results, failedCount)
func runCoverageJobs(
	ctx context.Context, repoRoot, modulePath string, jobs []goTestJob, workers int,
) ([]coverageResult, int) {
	jobCh := make(chan goTestJob)
	resCh := make(chan coverageResult)

	var failed int

	var failedMu sync.Mutex

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			for j := range jobCh {
				rows, err := runOneCoverageJob(ctx, repoRoot, modulePath, j, workerID)
				if err != nil {
					failedMu.Lock()
					failed++
					failedMu.Unlock()

					continue
				}

				resCh <- coverageResult{id: j.id(), rows: rows}
			}
		}(i)
	}

	go func() {
		for _, j := range jobs {
			jobCh <- j
		}

		close(jobCh)
	}()

	go func() {
		wg.Wait()
		close(resCh)
	}()

	var results []coverageResult
	for r := range resCh {
		results = append(results, r)
	}

	return results, failed
}

func runOneCoverageJob(
	ctx context.Context, repoRoot, modulePath string, j goTestJob, workerID int,
) ([]codeintel.CoverageRow, error) {
	prof := filepath.Join(os.TempDir(), fmt.Sprintf("wavez-cov-w%d-%s.out", workerID, sanitizeTestID(j.id())))
	defer func() { _ = os.Remove(prof) }() //nolint:errcheck // best-effort temp-file cleanup

	//nolint:gosec // args are this adapter's own generated test name and package path
	cmd := exec.CommandContext(ctx, "go", "test",
		"-run", "^"+j.name+"$",
		"-count=1",
		"-coverprofile="+prof,
		j.pkg,
	)
	cmd.Dir = repoRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		trimmed := truncateOutput(string(out), truncatedOutputLimit)

		return nil, fmt.Errorf("go test %s/%s: %w: %s", j.pkg, j.name, err, trimmed)
	}

	return parseCoverprofile(prof, modulePath)
}

func sanitizeTestID(id string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(id)
}

func truncateOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}

func parseCoverprofile(path, modulePath string) ([]codeintel.CoverageRow, error) {
	f, err := os.Open(path) //nolint:gosec // path is this adapter's own generated temp-file path
	if err != nil {
		return nil, fmt.Errorf("opening coverprofile %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle, nothing actionable on close failure

	var rows []codeintel.CoverageRow

	first := true

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false

			continue // "mode: <mode>" header line
		}

		fields := strings.Fields(line)
		if len(fields) != coverProfileFields {
			continue
		}

		count, err := strconv.Atoi(fields[2])
		if err != nil || count == 0 {
			continue
		}

		file, start, end, ok := parseCoverProfilePos(fields[0])
		if !ok {
			continue
		}

		rows = append(rows, codeintel.CoverageRow{
			File:  strings.TrimPrefix(file, modulePath+"/"),
			Start: start,
			End:   end,
		})
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading coverprofile %s: %w", path, err)
	}

	return rows, nil
}

// parseCoverProfilePos parses a coverprofile position of the form
// "file:startLine.startCol,endLine.endCol" into (file, start, end, ok).
//
//nolint:gocritic // named returns here would trip nonamedreturns instead; the doc comment above names them
func parseCoverProfilePos(pos string) (string, int, int, bool) {
	colon := strings.LastIndex(pos, ":")
	if colon < 0 {
		return "", 0, 0, false
	}

	file := pos[:colon]

	parts := strings.SplitN(pos[colon+1:], ",", coverProfilePosParts)
	if len(parts) != coverProfilePosParts {
		return "", 0, 0, false
	}

	startLine := strings.SplitN(parts[0], ".", coverProfilePosParts)[0]
	endLine := strings.SplitN(parts[1], ".", coverProfilePosParts)[0]

	s, err1 := strconv.Atoi(startLine)
	e, err2 := strconv.Atoi(endLine)

	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}

	return file, s, e, true
}
