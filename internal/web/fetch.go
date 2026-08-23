// Package web is the only place wavez reaches the public internet: one
// search and one fetch, both of them read-only, credential-free, and
// bounded.
//
// Everything it returns is untrusted. A page can carry text written to read
// as an instruction, and a run acts on tool results, so the defenses here
// are the deterministic ones that hold whatever the model believes: the
// request carries no credential and cannot be made to, the connection is
// refused if it resolves to a private address, a redirect may not change
// host, the body is capped, and the text is handed over inside a boundary
// that names it as data.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Limits every fetch is bound by.
const (
	// MaxBytes caps a response body. A page past it is truncated rather
	// than refused, since the head of a document is usually the answer.
	MaxBytes = 2 << 20
	// Timeout bounds one fetch, redirects included.
	Timeout = 20 * time.Second
	// The userAgent string identifies wavez rather than impersonating a
	// browser, because a site that does not want an agent should be able to
	// say so.
	userAgent = "wavez/1 (+https://github.com/kyleking/wavez)"

	maxRedirects = 5
)

// ErrSiteRefused reports a site that answered with an error status, which
// is the site's answer rather than this package's refusal.
var ErrSiteRefused = errors.New("the site refused the request")

// ErrNotFetchable reports a URL this package will not request, which is a
// refusal rather than a failure: nothing was sent.
var ErrNotFetchable = errors.New("that URL will not be fetched")

// ErrPrivateAddress reports a host that resolved to an address inside this
// machine or its network. It is checked when the connection is made rather
// than when the URL is parsed, so a name that resolves differently the
// second time is caught too.
var ErrPrivateAddress = errors.New("that host resolves inside this machine or its network")

// Page is one fetched document, already reduced to text.
type Page struct {
	URL   string
	Title string
	Text  string
	// Truncated reports that the body hit MaxBytes, so the text is a
	// prefix of the document rather than all of it.
	Truncated bool
}

// Fetcher performs bounded, credential-free GETs. The zero value is not
// usable; build one with NewFetcher.
type Fetcher struct {
	client *http.Client
}

// NewFetcher builds a Fetcher whose transport refuses private addresses and
// whose client carries no cookie jar, so nothing accumulates across calls
// and no credential can ride along with one.
func NewFetcher() *Fetcher {
	dialer := &net.Dialer{
		Timeout: Timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			return refusePrivate(address)
		},
	}

	return &Fetcher{client: &http.Client{
		Timeout:   Timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		Jar:       nil,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: it redirected more than %d times", ErrNotFetchable, maxRedirects)
			}

			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("%w: it redirects to %s, which is a different site from the one asked for",
					ErrNotFetchable, req.URL.Host)
			}

			return nil
		},
	}}
}

// Get fetches raw and reduces it to text. It sends no credential, no
// cookie, and no request body, and it is the only shape of request this
// package makes.
func (f *Fetcher) Get(ctx context.Context, raw string) (Page, error) {
	u, err := ParseFetchable(raw)
	if err != nil {
		return Page{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return Page{}, fmt.Errorf("building request for %s: %w", u.Host, err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.1")

	resp, err := f.client.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("fetching %s: %w", u.Host, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only response body

	if resp.StatusCode >= http.StatusBadRequest {
		return Page{}, fmt.Errorf("fetching %s: %w with %s", u.Host, ErrSiteRefused, resp.Status)
	}

	body, err := readCapped(resp.Body)
	if err != nil {
		return Page{}, fmt.Errorf("reading %s: %w", u.Host, err)
	}

	truncated := len(body) > MaxBytes
	if truncated {
		body = body[:MaxBytes]
	}

	title, text := ToText(string(body), resp.Header.Get("Content-Type"))

	return Page{URL: u.String(), Title: title, Text: text, Truncated: truncated}, nil
}

// readCapped reads at most one byte past MaxBytes, so a caller can tell a
// document that fit from one that was cut.
func readCapped(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}

	return body, nil
}

// ParseFetchable is every refusal that can be made from the URL alone. It
// is exported because a caller decides what to do about a host before
// anything is sent.
func ParseFetchable(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not a URL", ErrNotFetchable, raw)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: %s is not http or https", ErrNotFetchable, raw)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("%w: %s names no site", ErrNotFetchable, raw)
	}

	// Credentials in a URL are the one way this tool could be talked into
	// authenticating as somebody, so a URL carrying them is refused rather
	// than stripped: a caller that meant to send them should learn it did
	// not.
	if u.User != nil {
		return nil, fmt.Errorf("%w: %s carries a username or password, and this fetch never authenticates",
			ErrNotFetchable, u.Host)
	}

	if secret, found := SecretIn(u); found {
		return nil, fmt.Errorf("%w: its %s reads as a credential, and a fetch is a request to somebody else's server",
			ErrNotFetchable, secret)
	}

	return u, nil
}

// refusePrivate rejects an address inside this machine or its network,
// which is what stops a fetched page from turning into a request to the
// daemon's own socket, a metadata service, or anything else on the LAN.
func refusePrivate(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, address)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, address)
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, ip)
	}

	return nil
}
