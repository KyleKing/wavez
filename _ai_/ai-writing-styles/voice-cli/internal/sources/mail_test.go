package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeEmlx(t *testing.T, dir, name, rfc822 string) string {
	t.Helper()

	trailingPlist := "bplist00fakeplistdata"
	content := fmt.Sprintf("%d\n%s%s", len(rfc822), rfc822, trailingPlist)

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fake emlx: %v", err)
	}
	return path
}

func TestFetchMailPlainText(t *testing.T) {
	sentDir := filepath.Join(t.TempDir(), "Sent Messages.mbox", "Messages")
	if err := os.MkdirAll(sentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rfc822 := "From: kyle@example.com\r\n" +
		"Subject: test subject\r\n" +
		"Date: Mon, 2 Jan 2006 15:04:05 -0700\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"hello from the test\r\n"
	writeEmlx(t, sentDir, "1.emlx", rfc822)

	mailDir := filepath.Dir(filepath.Dir(sentDir))
	candidates, err := FetchMail(mailDir)
	if err != nil {
		t.Fatalf("FetchMail() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Context != "test subject" {
		t.Errorf("Context = %q, want %q", candidates[0].Context, "test subject")
	}
	if candidates[0].Text != "hello from the test" {
		t.Errorf("Text = %q, want %q", candidates[0].Text, "hello from the test")
	}
}

func TestFetchMailMultipart(t *testing.T) {
	sentDir := filepath.Join(t.TempDir(), "Sent Messages.mbox", "Messages")
	if err := os.MkdirAll(sentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	boundary := "boundary123"
	rfc822 := "From: kyle@example.com\r\n" +
		"Subject: multipart subject\r\n" +
		"Date: Mon, 2 Jan 2006 15:04:05 -0700\r\n" +
		"Content-Type: multipart/alternative; boundary=" + boundary + "\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"plain part body\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<p>html part body</p>\r\n" +
		"--" + boundary + "--\r\n"
	writeEmlx(t, sentDir, "2.emlx", rfc822)

	mailDir := filepath.Dir(filepath.Dir(sentDir))
	candidates, err := FetchMail(mailDir)
	if err != nil {
		t.Fatalf("FetchMail() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Text != "plain part body" {
		t.Errorf("Text = %q, want %q", candidates[0].Text, "plain part body")
	}
}

func TestFetchMailSkipsInbox(t *testing.T) {
	otherDir := filepath.Join(t.TempDir(), "Inbox.mbox", "Messages")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rfc822 := "From: kyle@example.com\r\nSubject: skip me\r\n\r\nbody\r\n"
	writeEmlx(t, otherDir, "1.emlx", rfc822)

	mailDir := filepath.Dir(filepath.Dir(otherDir))
	candidates, err := FetchMail(mailDir)
	if err != nil {
		t.Fatalf("FetchMail() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("len(candidates) = %d, want 0 (non-Sent dir should be skipped)", len(candidates))
	}
}
