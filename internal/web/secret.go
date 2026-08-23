package web

import (
	"net/url"
	"regexp"
	"strings"
)

// secretNames are query and path parameters whose value is a credential by
// convention. The check is on the name rather than the value, because a
// short token is indistinguishable from any other word and the name is what
// the caller wrote.
var secretNames = []string{
	"access_token", "api_key", "apikey", "auth", "authorization", "client_secret",
	"key", "password", "passwd", "private_key", "pwd", "secret", "session",
	"signature", "token",
}

// secretShapes are credentials recognizable wherever they appear, because
// each issuer publishes the prefix. They catch a key pasted into a path or
// a fragment, which no parameter name covers.
var secretShapes = regexp.MustCompile(
	`sk-[A-Za-z0-9_-]{16,}|` + // OpenAI and several others
		`sk-ant-[A-Za-z0-9_-]{16,}|` + // Anthropic
		`gh[pousr]_[A-Za-z0-9]{16,}|` + // GitHub
		`xox[abposr]-[A-Za-z0-9-]{10,}|` + // Slack
		`AKIA[0-9A-Z]{16}|` + // AWS access key id
		`AIza[0-9A-Za-z_-]{20,}|` + // Google
		`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.`, // a signed JWT
)

// SecretIn reports whether u carries something that reads as a credential,
// naming what it found rather than echoing it. A fetch is a request to
// somebody else's server, so anything in the URL is disclosed to them by
// definition, and this is the deterministic half of not disclosing a key.
//
// It reads the URL only. A secret the model puts somewhere this cannot see
// is not covered, which is why the tool sends no headers and no body at
// all.
func SecretIn(u *url.URL) (string, bool) {
	for name, values := range u.Query() {
		if !isSecretName(name) {
			continue
		}

		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return "`" + name + "` parameter", true
			}
		}
	}

	if secretShapes.MatchString(u.Path) || secretShapes.MatchString(u.RawQuery) ||
		secretShapes.MatchString(u.Fragment) {
		return "URL", true
	}

	return "", false
}

func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretNames {
		if lower == s || strings.HasSuffix(lower, "_"+s) || strings.HasSuffix(lower, "-"+s) {
			return true
		}
	}

	return false
}
