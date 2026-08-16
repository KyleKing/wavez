package buildfail

import "testing"

func TestBroken(t *testing.T) {
	_ = Broken()
}
