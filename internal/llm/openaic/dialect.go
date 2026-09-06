package openaic

// Dialect names the backend a Client dials. All of them speak
// /chat/completions over the same SSE framing, and each reads a few keys the
// others ignore: llama.cpp takes chat_template_kwargs and repeat_penalty,
// OpenRouter takes reasoning and provider, and Z.AI takes thinking. Sending
// every key to all of them works, because each drops what it does not know,
// and it hides which knob a tier actually has: a fast tier that turned
// reasoning off through chat_template_kwargs alone ran a hosted hybrid model
// at full reasoning length for four lanes without anything saying so.
type Dialect string

const (
	// DialectLlamaCpp is the loopback server internal/runtime supervises.
	DialectLlamaCpp Dialect = "llamacpp"
	// DialectOpenRouter is the hosted router the network tiers dial.
	DialectOpenRouter Dialect = "openrouter"
	// DialectZAI is Z.AI's coding-plan endpoint, which serves the GLM
	// models against a subscription rather than per token.
	DialectZAI Dialect = "zai"
)

// deniesDataCollection reports whether this dialect can be told to route
// only to providers that do not store prompts.
func (d Dialect) deniesDataCollection() bool { return d == DialectOpenRouter }

// readsReasoning reports whether this dialect spells the reasoning toggle
// as OpenRouter's `reasoning` object.
func (d Dialect) readsReasoning() bool { return d == DialectOpenRouter }

// readsThinkingType reports whether this dialect spells the reasoning
// toggle as Z.AI's `thinking` object, whose type is the string "enabled" or
// "disabled" rather than a boolean.
func (d Dialect) readsThinkingType() bool { return d == DialectZAI }

// readsChatTemplateKwargs reports whether this dialect spells the reasoning
// toggle as llama.cpp's per-request chat template override, which beats the
// server's own --chat-template-kwargs in both directions.
func (d Dialect) readsChatTemplateKwargs() bool { return d == DialectLlamaCpp }

// readsRepeatPenalty reports whether this dialect samples with llama.cpp's
// repeat_penalty, which is not an OpenAI parameter and which no OpenRouter
// endpoint of the models this project serves lists as supported.
func (d Dialect) readsRepeatPenalty() bool { return d == DialectLlamaCpp }

// compositionKeywords are the JSON Schema keywords whose value is a list of
// alternative subschemas. A dialect that rejects one of them has that
// subschema collapsed to its first branch, which is the shape every schema
// here states first and the one a caller can always satisfy.
var compositionKeywords = []string{"oneOf", "anyOf"}

// RejectedKeywords names the JSON Schema constructs this dialect's served
// models do not honor, at any depth of a tool's parameter schema. GLM-5.3
// honors neither composition keyword: given one it answers with `{}` in six
// completion tokens, and the same call with the schema flattened to a single
// branch answers with every argument.
//
// It is a declared set rather than a boolean because a deny list only holds
// what has already broken for somebody, and a keyword nobody has sent yet
// should be inert rather than fatal. `TestEveryToolSchemaSurvivesEveryDialect`
// is what stops the set from silently emptying a schema, since over-stripping
// is the failure this shape newly makes possible.
func (d Dialect) RejectedKeywords() []string {
	if d == DialectZAI {
		return compositionKeywords
	}

	return nil
}
