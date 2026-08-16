# aider chat started at 2026-07-10 14:32:11

> /add internal/foo/foo.go

#### Add error wrapping to the Parse function

internal/foo/foo.go
```go
<<<<<<< SEARCH
func Parse(s string) (Foo, error) {
	return doParse(s)
}
=======
func Parse(s string) (Foo, error) {
	f, err := doParse(s)
	if err != nil {
		return Foo{}, fmt.Errorf("parsing foo: %w", err)
	}
	return f, nil
}
>>>>>>> REPLACE
```

I added error wrapping so callers can use errors.Is/errors.As on failures.

> Tokens: 1.2k sent, 340 received.
