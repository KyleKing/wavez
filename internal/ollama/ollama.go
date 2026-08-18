// Package ollama talks to the local Ollama daemon and to the registry it
// pulls from. DESIGN.md keeps Ollama for pulling and listing models while
// llama-server does the serving, so this is the whole of wavez's model store:
// what is on disk, what the registry has, and the two deliberate actions
// (install, uninstall) a user may ask for.
package ollama

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is where Ollama listens unless a caller says otherwise.
const DefaultBaseURL = "http://127.0.0.1:11434"

// DefaultRegistryURL is the registry Ollama pulls library models from.
const DefaultRegistryURL = "https://registry.ollama.ai"

// ErrStatus reports a non-2xx response from Ollama or from the registry.
var ErrStatus = errors.New("ollama: unexpected status")

// Model is one model on disk, as Ollama's own tag listing reports it.
type Model struct {
	Modified time.Time
	// Name is the full "repo:tag" reference every other call takes.
	Name string
	// Digest is the sha256 of the manifest Ollama pulled, which is what an
	// update check compares against the registry.
	Digest        string
	Quant         string
	ParamSize     string
	Family        string
	SizeBytes     uint64
	ContextLength int
}

// Repo is the part of Name before the tag.
func (m Model) Repo() string {
	repo, _, _ := strings.Cut(m.Name, ":")

	return repo
}

// Tag is the part of Name after the colon, defaulting to "latest" the way
// Ollama itself does.
func (m Model) Tag() string {
	_, tag, ok := strings.Cut(m.Name, ":")
	if !ok || tag == "" {
		return "latest"
	}

	return tag
}

// Remote is what the registry holds for one reference.
type Remote struct {
	Digest    string
	SizeBytes uint64
}

// Client is one Ollama daemon plus the registry behind it.
type Client struct {
	http        *http.Client
	baseURL     string
	registryURL string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at an Ollama daemon other than the default.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithRegistryURL points update checks and size lookups at another registry.
func WithRegistryURL(url string) Option {
	return func(c *Client) { c.registryURL = strings.TrimRight(url, "/") }
}

// WithHTTPClient overrides the transport, which is how a test serves both
// endpoints from an httptest server.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New builds a Client against the local Ollama daemon and the public
// registry unless overridden.
func New(opts ...Option) *Client {
	c := &Client{
		http:        &http.Client{},
		baseURL:     DefaultBaseURL,
		registryURL: DefaultRegistryURL,
	}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

type tagsResponse struct {
	Models []tagsModel `json:"models"`
}

type tagsModel struct {
	Name     string     `json:"name"`
	Digest   string     `json:"digest"`
	Modified time.Time  `json:"modified_at"`
	Details  tagDetails `json:"details"`
	Size     uint64     `json:"size"`
}

type tagDetails struct {
	Family        string `json:"family"`
	ParameterSize string `json:"parameter_size"`
	Quantization  string `json:"quantization_level"`
	ContextLength int    `json:"context_length"`
}

// List reports every model Ollama has on disk.
func (c *Client) List(ctx context.Context) ([]Model, error) {
	body, err := c.do(ctx, http.MethodGet, c.baseURL+"/api/tags", nil, nil)
	if err != nil {
		return nil, err
	}

	var resp tagsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ollama: decoding tags: %w", err)
	}

	models := make([]Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		models = append(models, Model{
			Name:          m.Name,
			Digest:        m.Digest,
			Quant:         m.Details.Quantization,
			ParamSize:     m.Details.ParameterSize,
			Family:        m.Details.Family,
			ContextLength: m.Details.ContextLength,
			SizeBytes:     m.Size,
			Modified:      m.Modified,
		})
	}

	return models, nil
}

type manifest struct {
	Layers []manifestLayer `json:"layers"`
	Config manifestLayer   `json:"config"`
}

type manifestLayer struct {
	Size uint64 `json:"size"`
}

// Remote reads the registry's manifest for ref and reports its digest and
// the disk it would take. The digest is the sha256 of the manifest bytes,
// which is the same identity Ollama records locally, so comparing the two is
// the whole of the update check.
func (c *Client) Remote(ctx context.Context, ref string) (Remote, error) {
	body, err := c.do(ctx, http.MethodGet, c.manifestURL(ref), nil, map[string]string{
		"Accept": "application/vnd.docker.distribution.manifest.v2+json",
	})
	if err != nil {
		return Remote{}, err
	}

	var man manifest
	if err := json.Unmarshal(body, &man); err != nil {
		return Remote{}, fmt.Errorf("ollama: decoding manifest for %s: %w", ref, err)
	}

	size := man.Config.Size
	for _, l := range man.Layers {
		size += l.Size
	}

	sum := sha256.Sum256(body)

	return Remote{Digest: hex.EncodeToString(sum[:]), SizeBytes: size}, nil
}

// manifestURL maps a model reference to its registry path, defaulting an
// unqualified name into the library namespace the way Ollama does.
func (c *Client) manifestURL(ref string) string {
	repo, tag, ok := strings.Cut(ref, ":")
	if !ok || tag == "" {
		tag = "latest"
	}
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}

	return c.registryURL + "/v2/" + repo + "/manifests/" + tag
}

// Pull installs ref, blocking until Ollama reports the pull finished. Ollama
// streams progress as newline-delimited JSON, and the stream carrying an
// error object is the only way a failed pull is reported.
func (c *Client) Pull(ctx context.Context, ref string) error {
	req, err := buildRequest(ctx, http.MethodPost, c.baseURL+"/api/pull", pullBody(ref), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: pulling %s: %w", ref, err)
	}
	defer resp.Body.Close() //nolint:errcheck // the pull's own error is what a caller acts on

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return statusError("pulling "+ref, resp)
	}

	return scanPullStream(resp.Body, ref)
}

func scanPullStream(body io.Reader, ref string) error {
	dec := json.NewDecoder(body)

	for {
		var line struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}

		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("ollama: reading pull progress for %s: %w", ref, err)
		}
		if line.Error != "" {
			return fmt.Errorf("ollama: pulling %s: %w: %s", ref, ErrStatus, line.Error)
		}
	}
}

// Remove uninstalls ref. Ollama serves other tools on the same machine, so
// wavez only ever removes a model a user named.
func (c *Client) Remove(ctx context.Context, ref string) error {
	_, err := c.do(ctx, http.MethodDelete, c.baseURL+"/api/delete", pullBody(ref), nil)

	return err
}

func pullBody(ref string) []byte {
	b, err := json.Marshal(map[string]string{"model": ref})
	if err != nil {
		return nil
	}

	return b
}

func buildRequest(ctx context.Context, method, url string, body []byte, headers map[string]string) (
	*http.Request, error,
) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("ollama: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

func (c *Client) do(ctx context.Context, method, url string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := buildRequest(ctx, method, url, body, headers)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // the response body is fully read below

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, statusError(method+" "+url, resp)
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: reading %s: %w", url, err)
	}

	return out, nil
}

const maxErrorBody = 512

func statusError(what string, resp *http.Response) error {
	//nolint:errcheck // best-effort detail for the error message
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	return fmt.Errorf("ollama: %s: %w %d: %s", what, ErrStatus, resp.StatusCode, strings.TrimSpace(string(body)))
}
