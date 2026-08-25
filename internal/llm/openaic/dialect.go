package openaic

// Dialect names the backend a Client dials. Both speak /chat/completions
// over the same SSE framing, and each reads a few keys the other ignores:
// llama.cpp takes chat_template_kwargs and repeat_penalty, OpenRouter takes
// reasoning and provider. Sending every key to both works, because each
// drops what it does not know, and it hides which knob a tier actually has:
// a fast tier that turned reasoning off through chat_template_kwargs alone
// ran a hosted hybrid model at full reasoning length for four lanes without
// anything saying so.
type Dialect string

const (
	// DialectLlamaCpp is the loopback server internal/runtime supervises.
	DialectLlamaCpp Dialect = "llamacpp"
	// DialectOpenRouter is the hosted router the network tiers dial.
	DialectOpenRouter Dialect = "openrouter"
)

// deniesDataCollection reports whether this dialect can be told to route
// only to providers that do not store prompts.
func (d Dialect) deniesDataCollection() bool { return d == DialectOpenRouter }

// readsReasoning reports whether this dialect spells the reasoning toggle
// as OpenRouter's `reasoning` object.
func (d Dialect) readsReasoning() bool { return d == DialectOpenRouter }

// readsChatTemplateKwargs reports whether this dialect spells the reasoning
// toggle as llama.cpp's per-request chat template override, which beats the
// server's own --chat-template-kwargs in both directions.
func (d Dialect) readsChatTemplateKwargs() bool { return d == DialectLlamaCpp }

// readsRepeatPenalty reports whether this dialect samples with llama.cpp's
// repeat_penalty, which is not an OpenAI parameter and which no OpenRouter
// endpoint of the models this project serves lists as supported.
func (d Dialect) readsRepeatPenalty() bool { return d == DialectLlamaCpp }
