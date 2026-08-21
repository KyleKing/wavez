// Command fleet-probe drives a running wavezd over its socket: it opens one
// thread per root given, sends every prompt at once, and prints the schedule
// each time it changes until every thread is done.
//
// It exists because the three-thread condition in DESIGN's M2 row is proved
// on the fake-loop harness and has never been watched under a real model, and
// the TUI is otherwise the only client that can open three threads.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
)

type conn struct {
	c  net.Conn
	in *bufio.Scanner
	n  int
}

func dial(socket string) (*conn, error) {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", socket, err)
	}

	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	return &conn{c: c, in: sc}, nil
}

func (c *conn) send(cmd api.Command) error {
	c.n++
	cmd.ID = fmt.Sprintf("c%d", c.n)

	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", cmd.Kind, err)
	}

	if _, err := c.c.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("writing %s: %w", cmd.Kind, err)
	}

	return nil
}

// await reads until a reply of kind arrives, skipping the event stream a
// subscription interleaves with command replies.
func (c *conn) await(kind api.ReplyKind) (api.Reply, error) {
	for c.in.Scan() {
		var r api.Reply
		if err := json.Unmarshal(c.in.Bytes(), &r); err != nil {
			return r, fmt.Errorf("decoding reply: %w", err)
		}

		if r.Kind == api.RepError {
			return r, fmt.Errorf("server: %s", r.Error) //nolint:err113 // the server's own text is the error
		}

		if r.Kind == kind {
			return r, nil
		}
	}

	return api.Reply{}, fmt.Errorf("connection closed waiting for %s", kind) //nolint:err113 // terminal
}

func main() {
	socket := flag.String("socket", "", "wavezd socket path")
	prompt := flag.String("prompt", "", "prompt to send to every thread")
	model := flag.String("model", "", "tier to pin every thread to")
	every := flag.Duration("every", time.Second, "how often to poll the schedule")
	limit := flag.Duration("for", 10*time.Minute, "give up after this long")
	flag.Parse()

	if err := run(*socket, *prompt, *model, flag.Args(), *every, *limit); err != nil {
		fmt.Fprintln(os.Stderr, "fleet-probe:", err)
		os.Exit(1)
	}
}

func run(socket, prompt, model string, roots []string, every, limit time.Duration) error {
	if socket == "" || prompt == "" || len(roots) == 0 {
		return errUsage
	}

	c, err := dial(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.c.Close() }() //nolint:errcheck // cleanup

	if err := c.send(api.Command{Kind: api.CmdHello}); err != nil {
		return err
	}

	hello, err := c.await(api.RepHello)
	if err != nil {
		return err
	}

	if hello.Protocol != api.Protocol {
		return fmt.Errorf("server speaks protocol %d, this probe speaks %d", hello.Protocol, api.Protocol) //nolint:err113 // terminal
	}

	ids, err := open(c, roots, prompt, model)
	if err != nil {
		return err
	}

	return watch(c, ids, every, limit)
}

var errUsage = fmt.Errorf("usage: fleet-probe -socket <path> -prompt <text> <root> [<root>...]") //nolint:err113 // usage

// open starts one thread per root and sends every prompt before waiting on
// any of them, which is the whole point: three threads competing is what the
// scheduler is for.
func open(c *conn, roots []string, prompt, model string) ([]string, error) {
	ids := make([]string, 0, len(roots))

	for _, root := range roots {
		if err := c.send(api.Command{Kind: api.CmdNew, Root: root}); err != nil {
			return nil, err
		}

		r, err := c.await(api.RepThread)
		if err != nil {
			return nil, err
		}

		// The tier pin is route's Override. new's Model names a model and
		// reaches ThreadInfo only, so pinning there routes nothing.
		if model != "" {
			if err := c.send(api.Command{Kind: api.CmdRoute, ThreadID: r.Thread.ID, Override: router.Choice(model)}); err != nil {
				return nil, err
			}

			if _, err := c.await(api.RepThread); err != nil {
				return nil, err
			}
		}

		ids = append(ids, r.Thread.ID)

		fmt.Printf("opened %s (%s) in %s\n", r.Thread.Name, r.Thread.ID, root)
	}

	for _, id := range ids {
		if err := c.send(api.Command{Kind: api.CmdSend, ThreadID: id, Prompt: prompt}); err != nil {
			return nil, err
		}
	}

	fmt.Printf("sent %d prompts with no wait between them\n\n", len(ids))

	return ids, nil
}

func watch(c *conn, ids []string, every, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	last := ""
	// A lane is blank until the daemon picks the thread up, so a poll that
	// lands before the first one starts reads three blank lanes as three
	// finished ones. Only a thread seen working can be seen done.
	started := make(map[string]bool, len(ids))

	for time.Now().Before(deadline) {
		if err := c.send(api.Command{Kind: api.CmdSchedule}); err != nil {
			return err
		}

		r, err := c.await(api.RepSchedule)
		if err != nil {
			return err
		}

		if line := render(r.Schedule, ids); line != last {
			fmt.Print(line)

			last = line
		}

		if err := note(c, ids, started); err != nil {
			return err
		}

		over, err := settled(c, ids)
		if err != nil {
			return err
		}

		if len(started) == len(ids) && over {
			fmt.Println("every thread idle")

			return nil
		}

		time.Sleep(every)
	}

	return fmt.Errorf("gave up after %s", limit) //nolint:err113 // terminal
}

func render(s *api.Schedule, ids []string) string {
	if s == nil {
		return "no schedule\n"
	}

	var b strings.Builder

	free := 1.0
	if s.MemMeasured && s.MemTotalBytes > 0 {
		free = 1 - float64(s.MemUsedBytes)/float64(s.MemTotalBytes)
	}

	fmt.Fprintf(&b, "[%s] phase=%s model=%s free=%.0f%% headroom=%.0f%%\n",
		time.Now().Format("15:04:05"), s.Phase, s.LocalModel, 100*free, 100*s.Headroom)

	for _, id := range ids {
		l := laneFor(s, id)
		fmt.Fprintf(&b, "  %-22s %-40s", l.Thread, l.Step)

		if l.Lock != "" {
			fmt.Fprintf(&b, " lock %s <- %s", l.Lock, l.LockHolder)
		}

		if l.Gate != "" {
			fmt.Fprintf(&b, " gate %s", l.Gate)
		}

		b.WriteByte('\n')
	}

	for _, l := range s.Leases {
		fmt.Fprintf(&b, "  lease %s held by %s (%s)\n", l.Subtree, l.Holder, l.State)
	}

	return b.String()
}

func laneFor(s *api.Schedule, id string) api.Lane {
	for i := range s.Lanes {
		if s.Lanes[i].ThreadID == id {
			return s.Lanes[i]
		}
	}

	return api.Lane{Thread: id, Step: "(not on the schedule)"}
}

// note records every thread seen off idle at least once. Until then a poll
// that lands before the daemon picks the threads up reads three blank lanes
// as three finished ones.
// note records which of ids the daemon has picked up. The list carries every
// thread the daemon has open, including earlier runs left in a terminal
// state, so a count that does not filter to ids never matches len(ids).
func note(c *conn, ids []string, started map[string]bool) error {
	if err := c.send(api.Command{Kind: api.CmdList}); err != nil {
		return err
	}

	r, err := c.await(api.RepThreads)
	if err != nil {
		return err
	}

	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	for i := range r.Threads {
		if t := &r.Threads[i]; want[t.ID] && t.State != event.StateIdle {
			started[t.ID] = true
		}
	}

	return nil
}

func working(step string) bool {
	return step != "" && step != "idle" && step != "(not on the schedule)"
}

// settled asks the daemon for each thread's state rather than reading the
// lane, because a lane keeps the step it died on: a thread that failed shows
// its error there forever and never reads as finished.
func settled(c *conn, ids []string) (bool, error) {
	if err := c.send(api.Command{Kind: api.CmdList}); err != nil {
		return false, err
	}

	r, err := c.await(api.RepThreads)
	if err != nil {
		return false, err
	}

	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	for i := range r.Threads {
		t := &r.Threads[i]
		if want[t.ID] && t.State != event.StateDone && t.State != event.StateFailed &&
			t.State != event.StateIdle {
			return false, nil
		}
	}

	return true, nil
}
