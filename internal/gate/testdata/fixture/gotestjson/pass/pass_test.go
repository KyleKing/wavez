package pass

import "testing"

func TestOK(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("math is broken")
	}
}
