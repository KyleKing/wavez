package sources

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kyleking/voice-cli/internal/corpus"
)

// FetchMail walks mailDir (typically ~/Library/Mail) for .emlx files under a
// path containing "Sent", parsing each into a candidate. Multipart bodies get
// best-effort text/plain extraction, not full MIME fidelity.
func FetchMail(mailDir string) ([]corpus.Candidate, error) {
	var candidates []corpus.Candidate

	err := filepath.WalkDir(mailDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %q: %w", path, err)
		}
		if d.IsDir() || filepath.Ext(path) != ".emlx" {
			return nil
		}
		if !strings.Contains(path, "Sent") {
			return nil
		}

		c, parseErr := parseEmlxFile(path)
		if parseErr != nil {
			return fmt.Errorf("parsing %q: %w", path, parseErr)
		}
		candidates = append(candidates, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func parseEmlxFile(path string) (corpus.Candidate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return corpus.Candidate{}, fmt.Errorf("reading file: %w", err)
	}
	return parseEmlx(path, raw)
}

// parseEmlx decodes the .emlx container format: a decimal byte count on the
// first line, followed by exactly that many bytes of raw RFC822 message, then
// a trailing binary plist (attachment/flag metadata) that is discarded.
func parseEmlx(path string, raw []byte) (corpus.Candidate, error) {
	newline := bytes.IndexByte(raw, '\n')
	if newline < 0 {
		return corpus.Candidate{}, fmt.Errorf("no newline found in %q", path)
	}

	countStr := strings.TrimSpace(string(raw[:newline]))
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return corpus.Candidate{}, fmt.Errorf("parsing byte count %q: %w", countStr, err)
	}

	messageStart := newline + 1
	messageEnd := messageStart + count
	if messageEnd > len(raw) {
		return corpus.Candidate{}, fmt.Errorf("declared byte count %d exceeds file size", count)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw[messageStart:messageEnd]))
	if err != nil {
		return corpus.Candidate{}, fmt.Errorf("parsing RFC822 message: %w", err)
	}

	body, err := extractText(msg)
	if err != nil {
		return corpus.Candidate{}, fmt.Errorf("extracting body: %w", err)
	}

	subject := msg.Header.Get("Subject")
	timestamp, _ := msg.Header.Date()

	return corpus.Candidate{
		ID:        path,
		Source:    "mail",
		Author:    corpus.AuthorMe,
		Context:   subject,
		Timestamp: timestamp,
		Text:      body,
	}, nil
}

func extractText(msg *mail.Message) (string, error) {
	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// No/invalid Content-Type header: treat the whole body as plain text.
		body, readErr := io.ReadAll(msg.Body)
		if readErr != nil {
			return "", fmt.Errorf("reading plain body: %w", readErr)
		}
		return strings.TrimSpace(string(body)), nil
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		body, err := io.ReadAll(msg.Body)
		if err != nil {
			return "", fmt.Errorf("reading body: %w", err)
		}
		return strings.TrimSpace(string(body)), nil
	}

	boundary := params["boundary"]
	if boundary == "" {
		return "", fmt.Errorf("multipart message missing boundary")
	}

	reader := multipart.NewReader(msg.Body, boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading multipart part: %w", err)
		}

		partType := part.Header.Get("Content-Type")
		if partType == "" || strings.HasPrefix(partType, "text/plain") {
			body, err := io.ReadAll(part)
			if err != nil {
				return "", fmt.Errorf("reading part body: %w", err)
			}
			return strings.TrimSpace(string(body)), nil
		}
	}

	return "", nil
}
