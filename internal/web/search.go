package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// ErrNoResults reports a query that reached a search engine and came back
// empty, which is different from one that could not reach it at all.
var ErrNoResults = errors.New("that search returned nothing")

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Searcher runs a query and returns hits.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// NewSearcher picks the backend from configuration. A baseURL names a
// SearxNG instance, which is the option that aggregates several engines and
// keeps the queries on a host the user runs; empty falls back to
// DuckDuckGo's HTML endpoint, which needs no key and no service and is the
// one that breaks when their markup changes.
func NewSearcher(baseURL string, fetcher *Fetcher) Searcher { //nolint:ireturn // the backend is a configuration choice
	if strings.TrimSpace(baseURL) != "" {
		return &searx{base: strings.TrimRight(baseURL, "/"), fetcher: fetcher}
	}

	return &duck{fetcher: fetcher}
}

// duck queries DuckDuckGo's HTML endpoint, which answers without a key.
type duck struct{ fetcher *Fetcher }

func (d *duck) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	body, err := d.fetcher.raw(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	results := parseDuck(string(body), limit)
	if len(results) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoResults, query)
	}

	return results, nil
}

// parseDuck reads the result list out of DuckDuckGo's HTML. It is written
// against class names rather than document shape because the shape is what
// changes most often, and it returns nothing rather than guessing when it
// recognizes none of them.
func parseDuck(body string, limit int) []Result {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}

	var (
		out  []Result
		walk func(*html.Node)
		cur  Result
	)

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "result__a") {
			cur.Title = strings.TrimSpace(textOf(n))
			cur.URL = duckTarget(attr(n, "href"))
		}

		if n.Type == html.ElementNode && hasClass(n, "result__snippet") {
			cur.Snippet = strings.Join(strings.Fields(allText(n)), " ")
		}

		if cur.URL != "" && cur.Snippet != "" {
			if len(out) < limit {
				out = append(out, cur)
			}

			cur = Result{}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)

	if cur.URL != "" && len(out) < limit {
		out = append(out, cur)
	}

	return out
}

// duckTarget unwraps the redirect DuckDuckGo wraps every result in, so what
// reaches the model is the site's own URL and the provenance check that
// follows is about the site rather than about the search engine.
func duckTarget(href string) string {
	if href == "" {
		return ""
	}

	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}

	u, err := url.Parse(href)
	if err != nil {
		return ""
	}

	if target := u.Query().Get("uddg"); target != "" {
		return target
	}

	if u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}

	return ""
}

// searx queries a SearxNG instance's JSON API.
type searx struct {
	fetcher *Fetcher
	base    string
}

func (s *searx) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	endpoint := s.base + "/search?format=json&q=" + url.QueryEscape(query)

	body, err := s.fetcher.raw(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("reading the search instance's answer: %w", err)
	}

	out := make([]Result, 0, min(limit, len(payload.Results)))
	for _, r := range payload.Results {
		if len(out) == limit {
			break
		}

		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoResults, query)
	}

	return out, nil
}

// raw is Get without the text reduction, for the endpoints this package
// parses itself. It runs through the same refusals every fetch does.
func (f *Fetcher) raw(ctx context.Context, raw string) ([]byte, error) {
	u, err := ParseFetchable(raw)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", u.Host, err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching through %s: %w", u.Host, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only response body

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("searching through %s: %w with %s", u.Host, ErrSiteRefused, resp.Status)
	}

	body, err := readCapped(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u.Host, err)
	}

	return body, nil
}

func hasClass(n *html.Node, want string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == want {
			return true
		}
	}

	return false
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}

	return ""
}

func allText(n *html.Node) string {
	var b strings.Builder

	var walk func(*html.Node)

	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data + " ")
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(n)

	return b.String()
}
