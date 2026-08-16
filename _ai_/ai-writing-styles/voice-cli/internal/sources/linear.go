// Package sources implements the import-source fetchers: Linear, iMessage,
// and Apple Mail. Each fetcher returns []corpus.Candidate for the review step.
package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kyleking/voice-cli/internal/corpus"
)

const linearGraphQLURL = "https://api.linear.app/graphql"

// linearIssuesQuery pulls the most recent issues with their descriptions and
// comments. v1 fetches a single page (first: 50); deeper history requires
// cursor-based pagination, noted as a known limitation rather than built here.
const linearIssuesQuery = `
query RecentIssues {
  issues(first: 50, orderBy: updatedAt) {
    nodes {
      id
      identifier
      title
      description
      updatedAt
      creator { id name }
      comments(first: 50) {
        nodes {
          id
          body
          createdAt
          user { id name }
        }
      }
    }
  }
}`

type linearIssuesResponse struct {
	Data struct {
		Issues struct {
			Nodes []linearIssue `json:"nodes"`
		} `json:"issues"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type linearIssue struct {
	ID          string     `json:"id"`
	Identifier  string     `json:"identifier"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	Creator     linearUser `json:"creator"`
	Comments    struct {
		Nodes []linearComment `json:"nodes"`
	} `json:"comments"`
}

type linearComment struct {
	ID        string     `json:"id"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"createdAt"`
	User      linearUser `json:"user"`
}

type linearUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// graphQLDoer abstracts the HTTP call so tests can substitute an
// httptest.Server-backed client instead of hitting the real Linear API.
type graphQLDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// FetchLinear queries recent Linear issues and comments and partitions them
// into corpus.Candidate values tagged AuthorMe when the creator/commenter id
// matches meUserID, else AuthorTeam.
func FetchLinear(apiKey, meUserID string) ([]corpus.Candidate, error) {
	return fetchLinear(http.DefaultClient, linearGraphQLURL, apiKey, meUserID)
}

func fetchLinear(client graphQLDoer, url, apiKey, meUserID string) ([]corpus.Candidate, error) {
	body, err := json.Marshal(map[string]string{"query": linearIssuesQuery})
	if err != nil {
		return nil, fmt.Errorf("encoding linear graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building linear request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling linear api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear api returned status %d", resp.StatusCode)
	}

	var parsed linearIssuesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decoding linear response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("linear api error: %s", parsed.Errors[0].Message)
	}

	return partitionLinearIssues(parsed.Data.Issues.Nodes, meUserID), nil
}

func partitionLinearIssues(issues []linearIssue, meUserID string) []corpus.Candidate {
	var out []corpus.Candidate

	for _, issue := range issues {
		if issue.Description != "" {
			out = append(out, corpus.Candidate{
				ID:        issue.ID,
				Source:    "linear",
				Author:    authorFor(issue.Creator.ID, meUserID),
				Context:   fmt.Sprintf("%s: %s", issue.Identifier, issue.Title),
				Timestamp: issue.UpdatedAt,
				Text:      issue.Description,
				Tags:      []string{"issue-description"},
			})
		}
		for _, comment := range issue.Comments.Nodes {
			out = append(out, corpus.Candidate{
				ID:        comment.ID,
				Source:    "linear",
				Author:    authorFor(comment.User.ID, meUserID),
				Context:   fmt.Sprintf("%s: %s (comment)", issue.Identifier, issue.Title),
				Timestamp: comment.CreatedAt,
				Text:      comment.Body,
				Tags:      []string{"comment"},
			})
		}
	}
	return out
}

func authorFor(userID, meUserID string) corpus.Author {
	if userID == meUserID {
		return corpus.AuthorMe
	}
	return corpus.AuthorTeam
}
