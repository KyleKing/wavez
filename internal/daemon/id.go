package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func newID() string {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read does not fail on a supported platform; if it ever
		// does, degrade to something still unique for this process rather
		// than panicking mid-request.
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}

	return hex.EncodeToString(b)
}

const idBytes = 8
