package runtime

// DefaultContextSize is the served context llama-server starts with unless
// Config overrides it. DESIGN.md's Model routing section calls this a
// tuned number: raising it multiplies KV cache memory on a 16 GB machine.
const DefaultContextSize = 8192

// DefaultCacheReuse is the --cache-reuse token count llama-server starts
// with unless Config overrides it, matching the value measured in
// _ai_/demos/local-runtime.
const DefaultCacheReuse = 256

// DefaultBinary is the llama-server executable name resolved via PATH
// unless Config overrides it.
const DefaultBinary = "llama-server"

// thinkingOff disables a hybrid model's reasoning trace through the chat
// template. Measured on qwen3:8b: replying "OK" costs 92 completion tokens
// with it on and 3 with it off, and decode is the local bottleneck.
const thinkingOff = `{"enable_thinking":false}`

// Config configures one llama-server instance.
type Config struct {
	// GGUFPath is the model file llama-server loads with -m.
	GGUFPath string
	// Binary overrides the llama-server executable; DefaultBinary is used
	// when empty.
	Binary string
	// Port is the loopback port llama-server listens on.
	Port int
	// ContextSize is the served context in tokens; DefaultContextSize is
	// used when zero.
	ContextSize int
	// CacheReuse is the --cache-reuse token count; DefaultCacheReuse is
	// used when zero.
	CacheReuse int
}

func (c Config) binary() string {
	if c.Binary == "" {
		return DefaultBinary
	}

	return c.Binary
}

func (c Config) contextSize() int {
	if c.ContextSize == 0 {
		return DefaultContextSize
	}

	return c.ContextSize
}

func (c Config) cacheReuse() int {
	if c.CacheReuse == 0 {
		return DefaultCacheReuse
	}

	return c.CacheReuse
}
