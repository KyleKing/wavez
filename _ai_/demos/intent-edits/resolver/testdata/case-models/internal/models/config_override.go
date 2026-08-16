package models

// GitConfigOverride describes a git config key whose repo-local value differs
// from the value set globally for the same key.
type GitConfigOverride struct {
	Key         string
	LocalValue  string
	GlobalValue string
}
