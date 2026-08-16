package vcs

import "context"

// statusReader is a minimal stand-in for the real Operations interface,
// scoped to what identity.go needs, so this standalone module compiles
// without pulling in the rest of the vcs package.
type statusReader interface {
	GetRemoteURL(ctx context.Context, repoPath string) (string, error)
}

type stubOperations struct{}

func (stubOperations) GetRemoteURL(_ context.Context, _ string) (string, error) {
	return "", nil
}

// GetOperations is a stub matching the real factory.GetOperations signature
// closely enough for identity.go to compile and for RemoteIdentityFor to be
// exercised in isolation.
func GetOperations(_ string) statusReader {
	return stubOperations{}
}
