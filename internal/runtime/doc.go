// Package runtime manages llama-server as a child process and lists models
// Ollama already has on disk. DESIGN.md's Model routing section makes
// Wavez own the server process and the GGUF path (spec-type ngram-simple,
// cache-reuse, jinja, an explicit served context) while Ollama stays for
// pulling and listing models only, and only one server fits in 16 GB at a
// time.
package runtime
