package vcs

import (
	"strings"
)

// HostFromRemote extracts just the host portion of a git remote URL (the
// same host RemoteIdentity embeds as the first segment of its returned
// key), lowercased. A URL that names no resolvable host returns "".
func HostFromRemote(remoteURL string) string {
if strings.Contains(remoteURL, ":") {
		urlParts := strings.SplitN(remoteURL, ":", 2)
		if len(urlParts) > 0 {
			urlParts[0] = strings.ToLower(urlParts[0])
			if strings.Contains(urlParts[0], "/") {
				urlParts[0] = strings.Split(urlParts[0], "/")[0]
			}
			return urlParts[0]
		}
	}

	if strings.Contains(remoteURL, "@") {
		urlParts := strings.SplitN(remoteURL, "@", 2)
		if len(urlParts) > 0 {
			urlParts[0] = strings.ToLower(urlParts[0])
			if strings.Contains(urlParts[0], "/") {
				urlParts[0] = strings.Split(urlParts[0], "/")[0]
			}
			return urlParts[0]
		}
	}

	return ""
}
