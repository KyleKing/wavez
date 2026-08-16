package vcs

import (
	"strings"
)

// HostFromRemote extracts just the host portion of a git remote URL (the
// same host RemoteIdentity embeds as the first segment of its returned
// key), lowercased. A URL that names no resolvable host returns "".
func HostFromRemote(remoteURL string) string {
if strings.Contains(remoteURL, "://") {
		urlParts := strings.SplitN(remoteURL, "://", 2)
		if len(urlParts) < 2 {
			return ""
		}
		hostPart := urlParts[1]
		if strings.Contains(hostPart, "/") {
			hostPart = strings.SplitN(hostPart, "/", 2)[0]
		}
		return strings.ToLower(hostPart)
	}
	return ""
}
