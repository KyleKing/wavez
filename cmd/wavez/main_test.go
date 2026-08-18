package main

import (
	"testing"

	"github.com/kyleking/wavez/internal/config"
)

// linkifyText is the pure function behind `-p` text mode's markdown-link
// rendering; cmd/wavez has no CLI-level test harness, so this exercises it
// directly rather than shelling out to the built binary.
func TestLinkifyText(t *testing.T) {
	repo := []config.LinkPattern{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
	}

	t.Run("matched identifier becomes a markdown link", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		got := linkifyText(repo, "fixes #42 today")
		want := "fixes [#42](https://github.com/kyleking/wavez/pull/42) today"
		if got != want {
			t.Errorf("linkifyText() = %q, want %q", got, want)
		}
	})

	t.Run("no match leaves text unchanged", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		got := linkifyText(repo, "no identifiers here")
		if got != "no identifiers here" {
			t.Errorf("linkifyText() = %q, want the text unchanged", got)
		}
	})

	t.Run("invalid repo pattern leaves text unchanged", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		bad := []config.LinkPattern{{Pattern: `#(\d+`, URL: "https://example.com/$1"}}

		got := linkifyText(bad, "fixes #42 today")
		if got != "fixes #42 today" {
			t.Errorf("linkifyText() = %q, want the text unchanged on a bad pattern", got)
		}
	})
}
