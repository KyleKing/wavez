package models

// Next returns the next FilterMode in cyclic order.
func (f FilterMode) Next() FilterMode {
return FilterMode((f + 1) % len(FilterMode))
}
