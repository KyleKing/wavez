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
	"time"

	"github.com/kyleking/wavez/internal/codeintel"
)

const (
	manifestFilePerm     = 0o600
	truncatedOutputLimit = 300
	coverProfileFields   = 3
	coverProfilePosParts = 2
)

// CoverageWriter is the codeintel.Store method CoverageAdapter needs to
// record what it measured. A codeintel.Store satisfies it directly.
type CoverageWriter interface {
	WriteCoverage(ctx context.Context, testID, testHash string, rows []codeintel.CoverageRow) error
}

// CoverageStore is the store side of the coverage map: the writer the
// adapter fills and the reader Select queries. A codeintel.Store satisfies
// it directly.
type CoverageStore interface {
	CoverageWriter
	LineCoverage
}

// coverageMapGate names the coverage-map build in the gate log. It is not
// a Gate: it runs once in the background rather than per change event, and
// its entry carries no selection level for that reason.
const coverageMapGate = "coverage-map"

// CoverageAdapter runs Go's per-test coverprofile loop (DESIGN.md's Gates
// section) and writes rows into a codeintel store. It keeps its own
// manifest of which files each test last covered, by content hash,
// separate from the store's coverage rows, so a Refresh only re-runs a
// test whose covered files actually changed.
//
// It is also the LineCoverage selection reads, because only the thing that
// builds the map knows whether the map is finished: CoverageReady is false
// until every test in the module has been measured at least once, and a
// selection that trusted a half-built map would run three tests where the
// finished map names thirty.
type CoverageAdapter struct {
	store        CoverageStore
	log          *Log
	resources    *ResourceSet
	repoRoot     string
	manifestPath string
	workers      int
	// runMu holds one Refresh at a time; mu guards the readiness cache and
	// is never held across a test run.
	runMu    sync.Mutex
	mu       sync.Mutex
	complete bool
	loaded   bool
}

// CoverageOption configures a CoverageAdapter.
type CoverageOption func(*CoverageAdapter)

// WithCoverageLog records every build in the project's gate log, which is
// what keeps a map that failed to build visible instead of silent.
func WithCoverageLog(l *Log) CoverageOption {
	return func(a *CoverageAdapter) { a.log = l }
}

// WithCoverageResources makes the build compete for the process's shared
// resource keys, taking `go test` in shared mode per test so a gate run
// triggered by an edit preempts the build at the next test rather than
// waiting out the whole map.
func WithCoverageResources(res *ResourceSet) CoverageOption {
	return func(a *CoverageAdapter) { a.resources = res }
}

// NewCoverageAdapter builds an adapter over store for the module at
// repoRoot, persisting its re-run manifest at manifestPath and running up
// to workers tests in parallel.
func NewCoverageAdapter(
	store CoverageStore, repoRoot, manifestPath string, workers int, opts ...CoverageOption,
) *CoverageAdapter {
	if workers < 1 {
		workers = 1
	}

	a := &CoverageAdapter{store: store, repoRoot: repoRoot, manifestPath: manifestPath, workers: workers}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// coverageManifest is the adapter's own on-disk record of covered-file
// hashes per test. Complete says every test the module had at the end of
// the last build has an entry, which is what lets a later process know the
// map is usable without rebuilding it.
type coverageManifest struct {
	Tests    map[string]map[string]string `json:"tests"` // testID -> file -> content hash
	Complete bool                         `json:"complete"`
}

// RefreshStats reports what one Refresh call did.
type RefreshStats struct {
	Considered int
	Ran        int
	Skipped    int
	Failed     int
}

// Start builds the map in the background and returns immediately, the way
// codeintel.Indexer.Start absorbs its cold index cost: the first build is
// minutes (DESIGN.md measures 249 s for 522 Go tests at 8 workers) and
// selection stays at importer level until it lands. Later starts pay only
// for the tests whose covered files changed since the last one.
//
// It drops the error because the build reports itself to the gate log,
// which is the surface a failed map has to be visible on.
func (a *CoverageAdapter) Start(ctx context.Context) {
	go func() {
		start := time.Now()
		stats, err := a.Refresh(ctx)
		a.record(start, stats, err)
	}()
}

// CoverageReady reports whether the map covers every test in the module.
// It is false until a build has measured them all, which is what keeps a
// partial map from answering a selection query. A map built by an earlier
// process is ready without rebuilding, since the manifest records it.
func (a *CoverageAdapter) CoverageReady() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.loaded {
		man, err := loadManifest(a.manifestPath)
		if err != nil {
			return false
		}

		a.complete, a.loaded = man.Complete, true
	}

	return a.complete
}

// CoveringTests implements LineCoverage against the store the adapter
// fills.
func (a *CoverageAdapter) CoveringTests(
	ctx context.Context, file string, start, end int,
) ([]codeintel.CoverageTest, error) {
	//nolint:wrapcheck // a pass-through to the store, which already names the query in its error
	return a.store.CoveringTests(ctx, file, start, end)
}

// Refresh lists every Go test in the module and re-runs only the ones
// whose manifest entry is missing or whose previously covered files
// changed by content hash, writing fresh coverage rows for each into the
// store. Concurrent callers serialize rather than measuring twice.
func (a *CoverageAdapter) Refresh(ctx context.Context) (RefreshStats, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	man, err := loadManifest(a.manifestPath)
	if err != nil {
		return RefreshStats{}, err
	}

	modulePath, err := goModulePath(ctx, a.repoRoot)
	if err != nil {
		return RefreshStats{}, err
	}

	jobs, err := listGoTests(ctx, a.repoRoot)
	if err != nil {
		return RefreshStats{}, err
	}

	var stats RefreshStats

	stats.Considered = len(jobs)

	var toRun []goTestJob

	for _, j := range jobs {
		if testNeedsRerun(a.repoRoot, man, j.id()) {
			toRun = append(toRun, j)
		} else {
			stats.Skipped++
		}
	}

	pruned, err := a.prune(ctx, man, jobs)
	if err != nil {
		return stats, err
	}

	results, failed := runCoverageJobs(ctx, a.repoRoot, modulePath, toRun, a.workers, a.resources)
	stats.Ran = len(toRun)
	stats.Failed = failed

	if err := a.recordResults(ctx, man, results); err != nil {
		return stats, err
	}

	return stats, a.finish(man, jobs, pruned || len(results) > 0, failed == 0)
}

// prune drops tests the module no longer has. Their rows would otherwise
// outlive them and be selected forever, and `go test -run` against a name
// that no longer exists runs nothing at all.
func (a *CoverageAdapter) prune(ctx context.Context, man *coverageManifest, jobs []goTestJob) (bool, error) {
	live := make(map[string]struct{}, len(jobs))
	for _, j := range jobs {
		live[j.id()] = struct{}{}
	}

	pruned := false

	for id := range man.Tests {
		if _, ok := live[id]; ok {
			continue
		}

		if err := a.store.WriteCoverage(ctx, id, "", nil); err != nil {
			return pruned, fmt.Errorf("clearing coverage for removed test %s: %w", id, err)
		}

		delete(man.Tests, id)

		pruned = true
	}

	return pruned, nil
}

func (a *CoverageAdapter) recordResults(ctx context.Context, man *coverageManifest, results []coverageResult) error {
	for _, r := range results {
		hashes, err := hashCoveredFiles(a.repoRoot, r.rows)
		if err != nil {
			return err
		}

		if err := a.store.WriteCoverage(ctx, r.id, manifestDigest(hashes), r.rows); err != nil {
			return fmt.Errorf("writing coverage for %s: %w", r.id, err)
		}

		man.Tests[r.id] = hashes
	}

	return nil
}

// finish decides whether the map is now complete and persists that with
// the manifest. A test that would not run leaves the map incomplete even
// when every other test measured, because selection that quietly omits a
// test it could not measure is the wrong answer this readiness flag
// exists to prevent.
func (a *CoverageAdapter) finish(man *coverageManifest, jobs []goTestJob, wrote, allRan bool) error {
	complete := allRan && len(jobs) > 0

	for _, j := range jobs {
		if _, ok := man.Tests[j.id()]; !ok {
			complete = false

			break
		}
	}

	changed := man.Complete != complete
	man.Complete = complete

	a.mu.Lock()
	a.complete, a.loaded = complete, true
	a.mu.Unlock()

	if !wrote && !changed {
		return nil
	}

	return saveManifest(a.manifestPath, man)
}

// record writes the build's outcome to the gate log. It carries no
// selection level: the build is not a selection, and a level here would
// read as one in the log.
func (a *CoverageAdapter) record(start time.Time, stats RefreshStats, err error) {
	if a.log == nil {
		return
	}

	reason := fmt.Sprintf("considered %d, ran %d, skipped %d, failed %d",
		stats.Considered, stats.Ran, stats.Skipped, stats.Failed)
	if err != nil {
		reason = "build failed: " + err.Error()
	}

	entry := LogEntry{
		Timestamp: start,
		Gate:      coverageMapGate,
		Duration:  time.Since(start),
		Reason:    reason,
		Examined:  stats.Ran,
		Pass:      err == nil && stats.Failed == 0,
	}
	if logErr := a.log.Append(entry); logErr != nil {
		fmt.Fprintf(os.Stderr, "wavez: recording coverage map build: %v\n", logErr)
	}
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

func runCoverageJobs(
	ctx context.Context, repoRoot, modulePath string, jobs []goTestJob, workers int, res *ResourceSet,
) ([]coverageResult, int) {
	jobCh := make(chan goTestJob)
	resCh := make(chan coverageResult)

	var failed int

	var failedMu sync.Mutex

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := range jobCh {
				rows, err := runCoverageJob(ctx, repoRoot, modulePath, j, res)
				if err != nil {
					failedMu.Lock()
					failed++
					failedMu.Unlock()

					continue
				}

				resCh <- coverageResult{id: j.id(), rows: rows}
			}
		}()
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

// runCoverageJob takes the shared `go test` resource for one test only, so
// a gate run waits out a single test rather than the whole map.
func runCoverageJob(
	ctx context.Context, repoRoot, modulePath string, j goTestJob, res *ResourceSet,
) ([]codeintel.CoverageRow, error) {
	release := res.LockShared([]string{goTestResource})
	defer release()

	return runOneCoverageJob(ctx, repoRoot, modulePath, j)
}

func runOneCoverageJob(
	ctx context.Context, repoRoot, modulePath string, j goTestJob,
) ([]codeintel.CoverageRow, error) {
	// A profile path derived from the test name collides whenever two
	// builds measure the same module at once, and the loser parses the
	// other's file or an empty one, which reads as a test that covers
	// nothing rather than as a failure.
	f, err := os.CreateTemp("", "wavez-cov-*.out")
	if err != nil {
		return nil, fmt.Errorf("creating coverprofile for %s: %w", j.id(), err)
	}

	prof := f.Name()

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing coverprofile %s: %w", prof, err)
	}

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
