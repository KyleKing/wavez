package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/permission"
)

// dialDaemon connects to wavezd the way launchTUI does, so the inbox commands
// and the interface agree on which socket and which default project.
func dialDaemon(ctx context.Context, root, socket string) (*api.Client, error) {
	sock, err := resolveSocket(socket)
	if err != nil {
		return nil, err
	}

	client, err := api.Dial(ctx, sock, api.WithDefaultRoot(root))
	if err != nil {
		return nil, fmt.Errorf("no daemon at %s: %w (start one with `wavezd`)", sock, err)
	}

	return client, nil
}

func closeDaemon(client *api.Client) {
	if cerr := client.Close(); cerr != nil {
		fmt.Fprintf(os.Stderr, "wavez: closing connection: %v\n", cerr)
	}
}

// inboxRun prints one line per pending prompt across the fleet. It asks with
// CmdPending rather than waiting for a push, so a client that connects after
// every thread has parked still sees the list.
func inboxRun(ctx context.Context, root, socket string) error {
	client, err := dialDaemon(ctx, root, socket)
	if err != nil {
		return err
	}
	defer closeDaemon(client)

	rep, err := client.Do(ctx, api.Command{Kind: api.CmdPending})
	if err != nil {
		return fmt.Errorf("asking for pending prompts: %w", err)
	}

	return writePending(os.Stdout, rep.Pending, time.Now())
}

func writePending(w io.Writer, pending []api.PendingInfo, now time.Time) error {
	if len(pending) == 0 {
		if _, err := fmt.Fprintln(w, "no pending prompts"); err != nil {
			return fmt.Errorf("writing the pending list: %w", err)
		}

		return nil
	}

	for i := range pending {
		if _, err := fmt.Fprintf(w, "%s  %-12s  %-9s  wait %-6s  %s\n",
			pending[i].ID, pendingThread(pending[i]), pendingKind(pending[i]),
			now.Sub(pending[i].Asked).Round(time.Second),
			pendingDetail(pending[i])); err != nil {
			return fmt.Errorf("writing the pending list: %w", err)
		}
	}

	return nil
}

func pendingThread(p api.PendingInfo) string {
	if p.Thread != "" {
		return p.Thread
	}

	return p.ThreadID
}

func pendingKind(p api.PendingInfo) string {
	if p.Question {
		return "question"
	}

	return "permission"
}

// pendingDetail flattens a prompt onto one line, since a question can span
// several and a list row cannot.
func pendingDetail(p api.PendingInfo) string {
	detail := strings.Join(strings.Fields(p.Detail), " ")
	if p.Reason != "" {
		detail = strings.TrimSpace(p.Reason + " - " + detail)
	}

	return detail
}

// answerRun resolves one pending prompt by id. The -p text is a question's
// answer, and a permission prompt's decision read as a word.
func answerRun(ctx context.Context, root, socket, promptID, text string) error {
	client, err := dialDaemon(ctx, root, socket)
	if err != nil {
		return err
	}
	defer closeDaemon(client)

	rep, err := client.Do(ctx, api.Command{Kind: api.CmdPending})
	if err != nil {
		return fmt.Errorf("asking for pending prompts: %w", err)
	}

	var found *api.PendingInfo
	for i := range rep.Pending {
		if rep.Pending[i].ID == promptID {
			found = &rep.Pending[i]

			break
		}
	}
	if found == nil {
		return fmt.Errorf("%w: %s (see `wavez -inbox`)", errNoPending, promptID)
	}

	cmd := api.Command{Kind: api.CmdAnswer, PromptID: promptID}
	if found.Question {
		cmd.Answer = text
	} else {
		decision, derr := parseDecision(text)
		if derr != nil {
			return derr
		}
		cmd.Decision = decision
	}

	after, err := client.Do(ctx, cmd)
	if err != nil {
		return fmt.Errorf("answering %s: %w", promptID, err)
	}

	fmt.Printf("answered %s; %d prompts remain\n", promptID, len(after.Pending))

	return nil
}

// parseDecision reads a permission decision the way a person would type it.
func parseDecision(text string) (permission.Decision, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "allow", "yes", "y":
		return permission.Allow, nil
	case "deny", "no", "n":
		return permission.Deny, nil
	case "always", "allow_always":
		return permission.AllowAlways, nil
	default:
		return "", fmt.Errorf("%w: %q (want allow, deny, or always)", errUnknownDecision, text)
	}
}

// detachSubcommand resolves the root a detached thread belongs to and hands
// the rest to detachRun.
func detachSubcommand(ctx context.Context, opt options) error {
	root, err := resolveRoot(ctx, opt.dir)
	if err != nil {
		return err
	}

	return detachRun(ctx, root, opt.socket, opt)
}

// detachRun opens the thread in the daemon instead of running the prompt
// in-process, printing its id: the thread lives in wavezd, parks on a
// question, and the fleet's other threads keep working while the caller is
// away. Answer it later with `wavez -inbox` and `wavez -answer`.
func detachRun(ctx context.Context, root, socket string, opt options) error {
	if opt.prompt == "" {
		return errDetachNoPrompt
	}

	client, err := dialDaemon(ctx, root, socket)
	if err != nil {
		return err
	}
	defer closeDaemon(client)

	hint, err := routerHint(opt.model)
	if err != nil {
		return err
	}

	// A run stopped at a bound tells the caller another prompt continues it,
	// and -resume is how that prompt names the thread. Starting a fresh one
	// instead loses the transcript the message just promised was kept.
	if opt.resume != "" {
		if _, err := client.Do(ctx, api.Command{
			Kind: api.CmdSend, ThreadID: opt.resume, Prompt: opt.prompt,
		}); err != nil {
			return fmt.Errorf("continuing thread %s: %w", opt.resume, err)
		}

		fmt.Println(opt.resume)

		return nil
	}

	rep, err := client.Do(ctx, api.Command{
		Kind: api.CmdNew, Prompt: opt.prompt, Cycle: opt.cycle, Model: opt.model,
	})
	if err != nil {
		return fmt.Errorf("opening a thread in the daemon: %w", err)
	}
	if rep.Thread == nil {
		return fmt.Errorf("opening a thread in the daemon: %w", errReplyNoThread)
	}

	// A thread's tier pin is route's Override, not new's Model: Model names a
	// model and reaches ThreadInfo alone, so a pin here is what -model means.
	if hint.Override != "" {
		if _, err := client.Do(ctx, api.Command{
			Kind: api.CmdRoute, ThreadID: rep.Thread.ID, Override: hint.Override,
		}); err != nil {
			return fmt.Errorf("pinning thread %s to %s: %w", rep.Thread.ID, hint.Override, err)
		}
	}

	fmt.Println(rep.Thread.ID)

	return nil
}
