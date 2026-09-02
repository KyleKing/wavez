// Package lsp is Wavez's one generic language-server client: a Pool holds at
// most one server process per language per project root, started the first
// time a file that server handles is synced and reused by every later caller.
// DESIGN.md's Modifiers section asks for a single client behind rename and
// code actions, so nothing here is specific to diagnostics or to Go; the
// diagnostics gate is only its first consumer.
//
// The protocol layer is this package's own, over github.com/sourcegraph/jsonrpc2
// framing. It replaced github.com/charmbracelet/x/powernap, whose router reads
// a server request carrying id 0 as a notification and answers it with null:
// ty numbers its requests from zero and stops serving entirely once its
// configuration request is answered that way. The fix has been open upstream
// as charmbracelet/x#790 since March 2026 with no review, and wavez calls ten
// of that library's methods, so the transport is cheaper to own than to wait
// for. What is owned is the handshake, the four requests wavez sends, and the
// answers a server expects to its own requests; the wire types in wire.go
// carry only the fields those read.
package lsp
