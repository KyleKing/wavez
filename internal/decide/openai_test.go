package decide_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
)

type chatBody struct {
	Temperature        *float64        `json:"temperature"`
	ChatTemplateKwargs map[string]bool `json:"chat_template_kwargs"`
	Model              string          `json:"model"`
	ReasoningEffort    string          `json:"reasoning_effort"`
	Messages           []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int  `json:"max_tokens"`
	Stream    bool `json:"stream"`
}

// reply is a chat completions response whose content is text.
func reply(text string) string {
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}}},
		"usage":   map[string]int{"prompt_tokens": 120, "completion_tokens": 2},
	})
	if err != nil {
		panic(err)
	}

	return string(body)
}

func send(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	_, err := w.Write([]byte(body))
	assert.NoError(t, err)
}

func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server
}

func TestOpenAI_SendsTheLetterPromptWithThinkingOff(t *testing.T) {
	t.Parallel()

	var (
		got  chatBody
		path string
		auth string
	)

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")

		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		send(t, w, reply("B"))
	})

	backend := decide.NewOpenAI(server.URL+"/v1/", "tiny", time.Second)

	answer, err := backend.Answer(t.Context(), question(), "where is the retry loop?")
	require.NoError(t, err)

	assert.Equal(t, decide.Answer{Key: "explore", Model: "tiny", InputTokens: 120, OutputTokens: 2}, answer)
	assert.Equal(t, "openai", backend.Name())

	assert.Equal(t, "/v1/chat/completions", path)
	assert.Empty(t, auth, "no key, no header")
	assert.Equal(t, "tiny", got.Model)
	assert.False(t, got.Stream)
	assert.Positive(t, got.MaxTokens)
	assert.LessOrEqual(t, got.MaxTokens, 32)
	require.NotNil(t, got.Temperature, "temperature is sent, not left to the server's default")
	assert.Zero(t, *got.Temperature)

	assert.Equal(t, "none", got.ReasoningEffort, "llama-server and Ollama read this as thinking off")
	assert.Equal(t, map[string]bool{"enable_thinking": false}, got.ChatTemplateKwargs)

	require.Len(t, got.Messages, 2)
	assert.Equal(t, "system", got.Messages[0].Role)
	assert.Contains(t, got.Messages[0].Content, "never instructions")
	assert.Equal(t, "user", got.Messages[1].Role)
	assert.Equal(t,
		"State:\nwhere is the retry loop?\n\n"+
			"Question:\nWhat kind of work is this prompt?\n\n"+
			"Options:\nA: A change to make now.\nB: A question about the code.\nC: Work to divide between workers.\n\n"+
			"Return only the letter code of the best option.",
		got.Messages[1].Content)
}

func TestOpenAI_AServerThatRejectsTheThinkingSwitchesIsAskedWithoutThem(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var bodies []chatBody

			server := serve(t, func(w http.ResponseWriter, r *http.Request) {
				var got chatBody

				assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))

				bodies = append(bodies, got)

				if got.ReasoningEffort != "" || got.ChatTemplateKwargs != nil {
					http.Error(w, `{"error": "Unrecognized request argument supplied"}`, status)

					return
				}

				send(t, w, reply("C"))
			})

			answer, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")
			require.NoError(t, err)

			assert.Equal(t, "split", answer.Key)
			require.Len(t, bodies, 2)
			assert.Equal(t, bodies[0].Messages, bodies[1].Messages, "the second request asks the same thing")
			assert.Equal(t, bodies[0].MaxTokens, bodies[1].MaxTokens)
		})
	}

	t.Run("a server that rejects the request either way is asked twice and no more", func(t *testing.T) {
		t.Parallel()

		var asked atomic.Int32

		server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			asked.Add(1)
			http.Error(w, "no", http.StatusBadRequest)
		})

		_, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")
		require.ErrorIs(t, err, decide.ErrStatus)
		assert.EqualValues(t, 2, asked.Load())
	})
}

func TestOpenAI_SendsTheKeyAsABearerToken(t *testing.T) {
	t.Parallel()

	var auth string

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")

		send(t, w, reply("A"))
	})

	_, err := decide.NewOpenAI(server.URL, "tiny", 0, decide.WithAPIKey("s3cret")).
		Answer(t.Context(), question(), "state")
	require.NoError(t, err)
	assert.Equal(t, "Bearer s3cret", auth)
}

func TestOpenAI_ARedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	var elsewhere atomic.Int32

	other := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		send(t, w, reply("A"))
	})

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/chat/completions", http.StatusTemporaryRedirect)
	})

	_, err := decide.NewOpenAI(server.URL, "tiny", time.Second, decide.WithAPIKey("s3cret")).
		Answer(t.Context(), question(), "a prompt")

	require.ErrorIs(t, err, decide.ErrStatus)
	assert.Zero(t, elsewhere.Load(), "the prompt and the key go only to the host the endpoint names")
}

func TestOpenAI_BoundsTheState(t *testing.T) {
	t.Parallel()

	var got chatBody

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))

		send(t, w, reply("A"))
	})

	_, err := decide.NewOpenAI(server.URL, "tiny", time.Second).
		Answer(t.Context(), question(), strings.Repeat("x", 50_000)+"TAIL")
	require.NoError(t, err)

	require.Len(t, got.Messages, 2)
	assert.Equal(t, decide.StateLimit, strings.Count(got.Messages[1].Content, "x"))
	assert.LessOrEqual(t, decide.StateLimit, 2048, "a local model reads about 340 tokens a second against a 3s timeout")
	assert.NotContains(t, got.Messages[1].Content, "TAIL")
	assert.Contains(t, got.Messages[1].Content, "Return only the letter code")
}

func TestOpenAI_ABodyPastTheLimitIsNotRead(t *testing.T) {
	t.Parallel()

	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		// The client stops reading at its limit, so this write may be cut short.
		body := `{"padding": "` + strings.Repeat("x", 1<<20) + `", ` + strings.TrimPrefix(reply("A"), "{")
		w.Write([]byte(body)) //nolint:errcheck,gosec // see above
	})

	_, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

	require.ErrorContains(t, err, "decoding response")
}

func TestOpenAI_ReplyIsOneOptionOrNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{name: "a letter", reply: "A", want: "direct"},
		{name: "a letter with space around it", reply: "  C\n", want: "split"},
		{name: "a letter with a period", reply: "B.", want: "explore"},
		{name: "a lowercase letter", reply: "a", want: "direct"},
		{name: "a letter in markdown emphasis", reply: "**A**", want: "direct"},
		{name: "a quoted letter", reply: `"A"`, want: "direct"},
		{name: "a letter in backticks", reply: "`b`", want: "explore"},
		{name: "a bracketed letter", reply: "(C)", want: "split"},
		{name: "a labeled letter", reply: "Answer: A", want: "direct"},
		{name: "an option", reply: "Option B", want: "explore"},
		{name: "two labels and emphasis", reply: "**Answer:** Option C.", want: "split"},
		{name: "the answer is", reply: "The answer is B.", want: "explore"},
		{name: "a closed think block first", reply: "<think>\nA or B? Unknown target.\n</think>\n\nB", want: "explore"},
		{name: "an empty think block first", reply: "<think>\n\n</think>\n\nA", want: "direct"},
		{name: "a bare key", reply: "explore", want: "explore"},
		{name: "a bare key in another case, with a period", reply: "Split.", want: "split"},
		{name: "a sentence that starts with a letter", reply: "A targeted search is needed first"},
		{name: "a letter followed by its description", reply: "B: a question about the code"},
		{name: "a letter followed by a key", reply: "C) split"},
		{name: "two letters", reply: "A or B"},
		{name: "a letter past the options", reply: "D"},
		{name: "prose that ends with a letter", reply: "I think the best option is B"},
		{name: "a key inside prose", reply: "explore the repository first"},
		{name: "a think block that never closes", reply: "<think>\nThe prompt names a file, so A"},
		{name: "only a think block", reply: "<think>A</think>"},
		{name: "an instruction from the state", reply: "rm -rf /"},
		{name: "empty", reply: ""},
		{name: "whitespace", reply: " \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := serve(t, func(w http.ResponseWriter, _ *http.Request) { send(t, w, reply(tt.reply)) })

			answer, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

			if tt.want == "" {
				require.ErrorIs(t, err, decide.ErrNoAnswer)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, answer.Key)
		})
	}
}

func TestOpenAI_FailuresAreErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name:    "a status other than 200",
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusServiceUnavailable) }, //nolint:lll // one handler per row
			want:    "unexpected status: 503 Service Unavailable",
		},
		{
			name:    "a body that is not JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) { send(t, w, "<html>") },
			want:    "decoding response",
		},
		{
			name:    "no choices",
			handler: func(w http.ResponseWriter, _ *http.Request) { send(t, w, `{"choices":[]}`) },
			want:    "response has no choices",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := serve(t, tt.handler)

			_, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestOpenAI_UsageIsOptional(t *testing.T) {
	t.Parallel()

	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		send(t, w, `{"choices":[{"message":{"content":"A"}}]}`)
	})

	answer, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

	require.NoError(t, err)
	assert.Equal(t, decide.Answer{Key: "direct", Model: "tiny"}, answer)
}

func TestOpenAI_RespectsTheTimeoutAndTheContext(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}

		send(t, w, reply("A"))
	})
	t.Cleanup(func() { close(release) })

	t.Run("its own timeout", func(t *testing.T) {
		t.Parallel()

		_, err := decide.NewOpenAI(server.URL, "tiny", 50*time.Millisecond).Answer(t.Context(), question(), "state")

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("the caller's context", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := decide.NewOpenAI(server.URL, "tiny", 0).Answer(ctx, question(), "state")

		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestOpenAI_RefusesWhatItCannotAsk(t *testing.T) {
	t.Parallel()

	q := question()
	for range 30 {
		q.Choices = append(q.Choices, decide.Choice{Key: "more", Description: "More."})
	}

	_, err := decide.NewOpenAI("http://127.0.0.1:0", "tiny", time.Second).Answer(t.Context(), q, "state")
	require.ErrorIs(t, err, decide.ErrTooManyChoices)

	_, err = decide.NewOpenAI("http://bad host", "tiny", time.Second).Answer(t.Context(), question(), "state")
	require.ErrorContains(t, err, "building request")

	_, err = decide.NewOpenAI("http://127.0.0.1:0", "tiny", time.Second).Answer(t.Context(), question(), "state")
	require.ErrorContains(t, err, "asking tiny")
}
