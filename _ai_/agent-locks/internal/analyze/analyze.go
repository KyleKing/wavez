// Package analyze mines Claude Code transcripts for evidence about how often parallel
// sessions actually collide, and at what granularity. It is read-only.
package analyze

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/agent-locks/internal/bashwrite"
)

type Options struct {
	ProjectsDir string
	Window      time.Duration
	Since       time.Time
	TopN        int
	ExactOnly   bool
	ReposOnly   bool
}

type Write struct {
	TS        time.Time
	Session   string
	Sidechain bool
	Tool      string
	CWD       string
	Abs       string
	Dir       string
	ViaBash   bool
}

type Hotspot struct {
	Path       string   `json:"path"`
	Collisions int      `json:"collisions"`
	Sessions   []string `json:"sessions"`
}

type Report struct {
	Transcripts      int            `json:"transcripts"`
	Writes           int            `json:"writes"`
	ByTool           map[string]int `json:"by_tool"`
	FileToolWrites   int            `json:"file_tool_writes"`
	BashWrites       int            `json:"bash_writes"`
	BashSharePct     float64        `json:"bash_share_pct"`
	SidechainWrites  int            `json:"sidechain_writes"`
	OutsideCWD       int            `json:"outside_cwd"`
	OutsideCWDPct    float64        `json:"outside_cwd_pct"`
	DirCollisions    int            `json:"dir_collisions"`
	FileCollisions   int            `json:"file_collisions"`
	DirCollisionPct  float64        `json:"dir_collision_pct"`
	MultiSessionDirs int            `json:"multi_session_dirs"`
	// File-tool-only figures exclude the Bash heuristic, so they carry no false positives.
	ExactDirCollisions  int       `json:"exact_dir_collisions"`
	ExactFileCollisions int       `json:"exact_file_collisions"`
	ExactDirPct         float64   `json:"exact_dir_collision_pct"`
	OutsideRepo         int       `json:"outside_repo"`
	Window              string    `json:"window"`
	TopDirs             []Hotspot `json:"top_dirs"`
	TopFiles            []Hotspot `json:"top_files"`
}

type record struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"sessionId"`
	Timestamp   time.Time `json:"timestamp"`
	CWD         string    `json:"cwd"`
	IsSidechain bool      `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"input"`
}

var fileTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
}

func DefaultProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/projects"
	}
	return filepath.Join(home, ".claude", "projects")
}

func Run(opts Options) (*Report, error) {
	files, err := transcripts(opts.ProjectsDir)
	if err != nil {
		return nil, err
	}
	writes := scanAll(files, opts)
	writes, outside := filter(writes, opts)
	rep := summarize(writes, len(files), opts)
	rep.OutsideRepo = outside
	return rep, nil
}

func filter(writes []Write, opts Options) ([]Write, int) {
	outside := 0
	out := writes[:0]
	for _, w := range writes {
		if opts.ExactOnly && w.ViaBash {
			continue
		}
		if repoRoot(w.Dir) == "" {
			outside++
			if opts.ReposOnly {
				continue
			}
		}
		out = append(out, w)
	}
	return out, outside
}

var (
	rootCache = map[string]string{}
	rootMu    sync.Mutex
)

// repoRoot walks up for a .git entry, memoized because the corpus revisits the same
// directories thousands of times. An empty result means the write landed outside any
// repository, which is usually a scratch or config path.
func repoRoot(dir string) string {
	rootMu.Lock()
	defer rootMu.Unlock()
	if v, ok := rootCache[dir]; ok {
		return v
	}
	found := ""
	for d := dir; d != "" && d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			found = d
			break
		}
	}
	rootCache[dir] = found
	return found
}

func transcripts(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func scanAll(files []string, opts Options) []Write {
	jobs := make(chan string)
	results := make(chan []Write)
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				results <- scanFile(f, opts)
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var all []Write
	for r := range results {
		all = append(all, r...)
	}
	return all
}

func scanFile(path string, opts Options) []Write {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256<<10), 8<<20)

	var out []Write
	for sc.Scan() {
		var rec record
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "assistant" {
			continue
		}
		if !opts.Since.IsZero() && rec.Timestamp.Before(opts.Since) {
			continue
		}
		var blocks []block
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			out = append(out, writesFor(rec, b)...)
		}
	}
	return out
}

func writesFor(rec record, b block) []Write {
	base := Write{
		TS:        rec.Timestamp,
		Session:   rec.SessionID,
		Sidechain: rec.IsSidechain,
		Tool:      b.Name,
		CWD:       rec.CWD,
	}
	switch {
	case fileTools[b.Name]:
		p := b.Input.FilePath
		if p == "" {
			p = b.Input.NotebookPath
		}
		if p == "" {
			return nil
		}
		return []Write{located(base, p)}
	case b.Name == "Bash":
		res := bashwrite.Detect(b.Input.Command)
		if !res.IsWrite {
			return nil
		}
		base.ViaBash = true
		if len(res.Paths) == 0 {
			return []Write{located(base, rec.CWD)}
		}
		var out []Write
		for _, p := range res.Paths {
			out = append(out, located(base, p))
		}
		return out
	}
	return nil
}

func located(w Write, p string) Write {
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.CWD, p)
	}
	w.Abs = filepath.Clean(p)
	w.Dir = filepath.Dir(w.Abs)
	return w
}

func summarize(writes []Write, transcripts int, opts Options) *Report {
	rep := &Report{
		Transcripts: transcripts,
		Writes:      len(writes),
		ByTool:      map[string]int{},
		Window:      opts.Window.String(),
	}
	for _, w := range writes {
		rep.ByTool[w.Tool]++
		if w.ViaBash {
			rep.BashWrites++
		} else {
			rep.FileToolWrites++
		}
		if w.Sidechain {
			rep.SidechainWrites++
		}
		if w.CWD != "" && !within(w.Abs, w.CWD) {
			rep.OutsideCWD++
		}
	}
	rep.BashSharePct = pct(rep.BashWrites, rep.Writes)
	rep.OutsideCWDPct = pct(rep.OutsideCWD, rep.Writes)

	dirHits, dirSessions := collisions(writes, func(w Write) string { return w.Dir }, opts.Window)
	fileHits, _ := collisions(writes, func(w Write) string { return w.Abs }, opts.Window)
	rep.DirCollisions = total(dirHits)
	rep.FileCollisions = total(fileHits)
	rep.DirCollisionPct = pct(rep.DirCollisions, rep.Writes)

	var exact []Write
	for _, w := range writes {
		if !w.ViaBash {
			exact = append(exact, w)
		}
	}
	exactDir, _ := collisions(exact, func(w Write) string { return w.Dir }, opts.Window)
	exactFile, _ := collisions(exact, func(w Write) string { return w.Abs }, opts.Window)
	rep.ExactDirCollisions = total(exactDir)
	rep.ExactFileCollisions = total(exactFile)
	rep.ExactDirPct = pct(rep.ExactDirCollisions, len(exact))
	for _, s := range dirSessions {
		if len(s) > 1 {
			rep.MultiSessionDirs++
		}
	}
	rep.TopDirs = top(dirHits, dirSessions, opts.TopN)
	rep.TopFiles = top(fileHits, nil, opts.TopN)
	return rep
}

// collisions counts writes that landed within the window of a write by a different
// session against the same key.
func collisions(writes []Write, key func(Write) string, window time.Duration) (map[string]int, map[string]map[string]bool) {
	grouped := map[string][]Write{}
	for _, w := range writes {
		k := key(w)
		grouped[k] = append(grouped[k], w)
	}
	hits := map[string]int{}
	sessions := map[string]map[string]bool{}
	for k, ws := range grouped {
		sessions[k] = map[string]bool{}
		for _, w := range ws {
			sessions[k][w.Session] = true
		}
		if len(sessions[k]) < 2 {
			continue
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].TS.Before(ws[j].TS) })
		for i, w := range ws {
			for j := i - 1; j >= 0; j-- {
				if w.TS.Sub(ws[j].TS) > window {
					break
				}
				if ws[j].Session != w.Session {
					hits[k]++
					break
				}
			}
		}
	}
	return hits, sessions
}

func top(hits map[string]int, sessions map[string]map[string]bool, n int) []Hotspot {
	var out []Hotspot
	for k, v := range hits {
		if v == 0 {
			continue
		}
		h := Hotspot{Path: k, Collisions: v}
		for s := range sessions[k] {
			h.Sessions = append(h.Sessions, s)
		}
		sort.Strings(h.Sessions)
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Collisions != out[j].Collisions {
			return out[i].Collisions > out[j].Collisions
		}
		return out[i].Path < out[j].Path
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}
