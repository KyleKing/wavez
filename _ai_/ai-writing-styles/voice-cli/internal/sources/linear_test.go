package sources

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyleking/voice-cli/internal/corpus"
)

const fakeLinearResponse = `{
  "data": {
    "issues": {
      "nodes": [
        {
          "id": "issue-1",
          "identifier": "ENG-1",
          "title": "Fix the thing",
          "description": "This is my description",
          "updatedAt": "2026-01-01T00:00:00Z",
          "creator": {"id": "me-id", "name": "Kyle"},
          "comments": {
            "nodes": [
              {"id": "comment-1", "body": "my comment", "createdAt": "2026-01-02T00:00:00Z", "user": {"id": "me-id", "name": "Kyle"}},
              {"id": "comment-2", "body": "teammate comment", "createdAt": "2026-01-03T00:00:00Z", "user": {"id": "team-id", "name": "Alex"}}
            ]
          }
        }
      ]
    }
  }
}`

func TestFetchLinearPartitionsByAuthor(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakeLinearResponse))
	}))
	defer server.Close()

	candidates, err := fetchLinear(server.Client(), server.URL, "test-api-key", "me-id")
	if err != nil {
		t.Fatalf("fetchLinear() error = %v", err)
	}

	if gotAuth != "test-api-key" {
		t.Errorf("Authorization header = %q, want %q (no Bearer prefix)", gotAuth, "test-api-key")
	}

	if len(candidates) != 3 {
		t.Fatalf("len(candidates) = %d, want 3", len(candidates))
	}

	byID := make(map[string]corpus.Candidate, len(candidates))
	for _, c := range candidates {
		byID[c.ID] = c
	}

	if byID["issue-1"].Author != corpus.AuthorMe {
		t.Errorf("issue-1 author = %q, want me", byID["issue-1"].Author)
	}
	if byID["comment-1"].Author != corpus.AuthorMe {
		t.Errorf("comment-1 author = %q, want me", byID["comment-1"].Author)
	}
	if byID["comment-2"].Author != corpus.AuthorTeam {
		t.Errorf("comment-2 author = %q, want team", byID["comment-2"].Author)
	}
}

func TestFetchLinearErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"errors": []map[string]string{{"message": "invalid api key"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	_, err := fetchLinear(server.Client(), server.URL, "bad-key", "me-id")
	if err == nil {
		t.Fatal("expected error for graphql error response, got nil")
	}
}
