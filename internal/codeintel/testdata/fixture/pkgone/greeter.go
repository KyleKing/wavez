// Package pkgone is a tiny fixture for symbol extraction tests.
package pkgone

// Greeter says hello to someone by name.
type Greeter struct {
	Prefix string
}

// Greet returns a greeting for name.
func (g *Greeter) Greet(name string) string {
	return g.Prefix + name
}

// NewGreeter builds a Greeter with the given prefix.
func NewGreeter(prefix string) *Greeter {
	return &Greeter{Prefix: prefix}
}
