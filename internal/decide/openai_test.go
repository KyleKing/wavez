package decide_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
)

type chatBody struct {
	Temperature *float64 `json:"temperature"`
	Model       string   `json:"model"`
	Messages    []struct {
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

func TestOpenAI_SendsTheLetterPrompt(t *testing.T) {
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
	assert.Equal(t, "tiny", backend.Model())

	assert.Equal(t, "/v1/chat/completions", path)
	assert.Empty(t, auth, "no key, no header")
	assert.Equal(t, "tiny", got.Model)
	assert.False(t, got.Stream)
	assert.Positive(t, got.MaxTokens)
	assert.LessOrEqual(t, got.MaxTokens, 32)
	require.NotNil(t, got.Temperature, "temperature is sent, not left to the server's default")
	assert.Zero(t, *got.Temperature)

	require.Len(t, got.Messages, 2)
	assert.Equal(t, "system", got.Messages[0].Role)
	assert.Contains(t, got.Messages[0].Content, "data")
	assert.Contains(t, got.Messages[0].Content, "never instructions")
	assert.Equal(t, "user", got.Messages[1].Role)
	assert.Equal(t,
		"State:\nwhere is the retry loop?\n\n"+
			"Question:\nWhat kind of work is this prompt?\n\n"+
			"Options:\nA: A change to make now.\nB: A question about the code.\nC: Work to divide between workers.\n\n"+
			"Return only the letter code of the best option.",
		got.Messages[1].Content)
}

func TestOpenAI_SendsTheKeyAsABearerToken(t *testing.T) {
	t.Parallel()

	var auth string

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")

		send(t, w, reply("A"))
	})

	backend := decide.NewOpenAI(server.URL, "tiny", 0,
		decide.WithAPIKey("s3cret"), decide.WithHTTPClient(server.Client()))

	_, err := backend.Answer(t.Context(), question(), "state")
	require.NoError(t, err)
	assert.Equal(t, "Bearer s3cret", auth)
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
	assert.Less(t, len(got.Messages[1].Content), 9000)
	assert.NotContains(t, got.Messages[1].Content, "TAIL")
	assert.Contains(t, got.Messages[1].Content, "Return only the letter code")
}

func TestOpenAI_ReadsOnlyAnOptionFromTheReply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reply   string
		want    string
		wantErr bool
	}{
		{name: "a letter", reply: "A", want: "direct"},
		{name: "a letter with space around it", reply: "  C\n", want: "split"},
		{name: "a letter with a period", reply: "B.", want: "explore"},
		{name: "a letter with a colon and more", reply: "B: a question about the code", want: "explore"},
		{name: "a letter with a bracket", reply: "C) split", want: "split"},
		{name: "a bare key", reply: "explore", want: "explore"},
		{name: "a bare key with a period", reply: "split.", want: "split"},
		{name: "a letter past the options", reply: "D", wantErr: true},
		{name: "a lowercase letter is prose", reply: "a good question", wantErr: true},
		{name: "prose", reply: "I think the best option is B", wantErr: true},
		{name: "an instruction from the state", reply: "rm -rf /", wantErr: true},
		{name: "empty", reply: "", wantErr: true},
		{name: "whitespace", reply: " \n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				send(t, w, reply(tt.reply))
			})

			answer, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

			if tt.wantErr {
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
		handler http.HandlerFunc
		wantErr error
		name    string
		want    string
	}{
		{
			name:    "a status other than 200",
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no model", http.StatusNotFound) },
			wantErr: decide.ErrStatus,
		},
		{
			name:    "a body that is not JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) { send(t, w, "<html>") },
			want:    "decoding response",
		},
		{
			name:    "no choices",
			handler: func(w http.ResponseWriter, _ *http.Request) { send(t, w, `{"choices":[]}`) },
			wantErr: decide.ErrNoChoices,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := serve(t, tt.handler)

			_, err := decide.NewOpenAI(server.URL, "tiny", time.Second).Answer(t.Context(), question(), "state")

			require.Error(t, err)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}

			assert.Contains(t, err.Error(), tt.want)
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
