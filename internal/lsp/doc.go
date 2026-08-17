// Package lsp is Wavez's one generic language-server client: a Pool holds at
// most one server process per language per project root, started the first
// time a file that server handles is synced and reused by every later caller.
// DESIGN.md's Modifiers section asks for a single client behind rename and
// code actions, so nothing here is specific to diagnostics or to Go; the
// diagnostics gate is only its first consumer.
//
// The protocol layer is github.com/charmbracelet/x/powernap, the client
// Crush drives gopls with. DESIGN.md names go.lsp.dev/protocol instead, which
// shipped a v1.0.0 rewrite in June 2026 but declares "go 1.26" and offers
// types without process management; powernap declares "go 1.24", spawns and
// reaps the server, and already carries the rename, references, and symbol
// requests M3 needs.
package lsp
