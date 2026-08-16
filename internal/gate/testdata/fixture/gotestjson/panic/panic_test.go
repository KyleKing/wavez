package panic_test

import "testing"

func TestPanics(t *testing.T) {
	var m map[string]int
	m["x"] = 1
	_ = m
}
