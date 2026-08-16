// buildstore builds a line-to-test coverage map and an import graph for a Go
// repo, writing CSV files that get loaded into a SQLite store.
package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pkgInfo struct {
	ImportPath   string
	Dir          string
	Imports      []string
	Deps         []string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
}

type job struct {
	pkg  string
	test string
}

type covRow struct {
	file  string
	start int
	end   int
	test  string
}

func main() {
	repo := flag.String("repo", "", "path to target go repo")
	workers := flag.Int("workers", 8, "parallel worker count for per-test coverage runs")
	outDir := flag.String("out", "", "output dir for csv files")
	metaOnly := flag.Bool("meta-only", false, "only write imports.csv and file_pkg.csv, skip coverage runs")
	flag.Parse()
	if *repo == "" || *outDir == "" {
		log.Fatal("-repo and -out are required")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	pkgs, err := listPackages(*repo)
	if err != nil {
		log.Fatalf("go list: %v", err)
	}
	fmt.Printf("found %d packages\n", len(pkgs))

	if err := writeImports(pkgs, filepath.Join(*outDir, "imports.csv")); err != nil {
		log.Fatalf("writeImports: %v", err)
	}
	if err := writeFilePkg(pkgs, filepath.Join(*outDir, "file_pkg.csv")); err != nil {
		log.Fatalf("writeFilePkg: %v", err)
	}
	if *metaOnly {
		return
	}

	jobs, err := listTests(*repo, pkgs)
	if err != nil {
		log.Fatalf("listTests: %v", err)
	}
	fmt.Printf("found %d tests across %d packages\n", len(jobs), len(pkgs))

	start := time.Now()
	rows, testErrs := runCoverage(*repo, jobs, *workers)
	elapsed := time.Since(start)
	fmt.Printf("ran %d per-test coverage jobs in %s (%d failed)\n", len(jobs), elapsed, testErrs)

	if err := writeCoverage(rows, filepath.Join(*outDir, "coverage.csv")); err != nil {
		log.Fatalf("writeCoverage: %v", err)
	}
	fmt.Printf("wrote %d coverage rows\n", len(rows))
	fmt.Printf("TOTAL_WALL_SECONDS=%.2f\n", elapsed.Seconds())
}

func listPackages(repo string) ([]pkgInfo, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var pkgs []pkgInfo
	for dec.More() {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func writeImports(pkgs []pkgInfo, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"src_pkg", "dst_pkg"}); err != nil {
		return err
	}
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			if err := w.Write([]string{p.ImportPath, imp}); err != nil {
				return err
			}
		}
	}
	return nil
}

var testNameRe = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)

func listTests(repo string, pkgs []pkgInfo) ([]job, error) {
	var jobs []job
	for _, p := range pkgs {
		if len(p.TestGoFiles) == 0 && len(p.XTestGoFiles) == 0 {
			continue
		}
		cmd := exec.Command("go", "test", "-list", ".*", p.ImportPath)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			// package may fail to build; skip but report
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", p.ImportPath, err)
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if testNameRe.MatchString(line) {
				jobs = append(jobs, job{pkg: p.ImportPath, test: line})
			}
		}
	}
	return jobs, nil
}

func runCoverage(repo string, jobs []job, workers int) ([]covRow, int) {
	jobCh := make(chan job)
	rowCh := make(chan []covRow)
	errCount := 0
	var errMu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range jobCh {
				rows, err := runOneTest(repo, j, id)
				if err != nil {
					errMu.Lock()
					errCount++
					errMu.Unlock()
					fmt.Fprintf(os.Stderr, "FAIL %s/%s: %v\n", j.pkg, j.test, err)
					continue
				}
				rowCh <- rows
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
		close(rowCh)
	}()

	var all []covRow
	for rows := range rowCh {
		all = append(all, rows...)
	}
	return all, errCount
}

func runOneTest(repo string, j job, workerID int) ([]covRow, error) {
	profPath := filepath.Join(os.TempDir(), fmt.Sprintf("covrun-w%d-%s.out", workerID, sanitize(j.test)))
	defer os.Remove(profPath)

	cmd := exec.Command("go", "test",
		"-run", "^"+j.test+"$",
		"-count=1",
		"-coverprofile="+profPath,
		j.pkg,
	)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, truncate(string(out), 300))
	}
	return parseCoverprofile(profPath, j.test)
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parseCoverprofile reads a go coverprofile file and returns one row per
// covered statement block (count > 0), attributed to the given test.
func parseCoverprofile(path, test string) ([]covRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []covRow
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue // mode: line
		}
		// format: name:startLine.startCol,endLine.endCol numStmt count
		spaceParts := strings.Fields(line)
		if len(spaceParts) != 3 {
			continue
		}
		count, err := strconv.Atoi(spaceParts[2])
		if err != nil || count == 0 {
			continue
		}
		posPart := spaceParts[0]
		colonIdx := strings.LastIndex(posPart, ":")
		if colonIdx < 0 {
			continue
		}
		file := posPart[:colonIdx]
		rangePart := posPart[colonIdx+1:]
		commaRanges := strings.SplitN(rangePart, ",", 2)
		if len(commaRanges) != 2 {
			continue
		}
		startLine := strings.SplitN(commaRanges[0], ".", 2)[0]
		endLine := strings.SplitN(commaRanges[1], ".", 2)[0]
		s, err1 := strconv.Atoi(startLine)
		e, err2 := strconv.Atoi(endLine)
		if err1 != nil || err2 != nil {
			continue
		}
		rows = append(rows, covRow{file: file, start: s, end: e, test: test})
	}
	return rows, sc.Err()
}

func writeFilePkg(pkgs []pkgInfo, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"file", "pkg"}); err != nil {
		return err
	}
	for _, p := range pkgs {
		for _, gf := range append(append([]string{}, p.GoFiles...), append(p.TestGoFiles, p.XTestGoFiles...)...) {
			if err := w.Write([]string{p.ImportPath + "/" + gf, p.ImportPath}); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeCoverage(rows []covRow, path string) error {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].file != rows[j].file {
			return rows[i].file < rows[j].file
		}
		if rows[i].start != rows[j].start {
			return rows[i].start < rows[j].start
		}
		return rows[i].test < rows[j].test
	})
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"file", "start_line", "end_line", "test"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.file, strconv.Itoa(r.start), strconv.Itoa(r.end), r.test}); err != nil {
			return err
		}
	}
	return nil
}
