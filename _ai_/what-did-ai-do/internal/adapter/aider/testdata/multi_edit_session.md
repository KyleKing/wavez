# aider chat started at 2026-07-11 09:00:00

#### Split the validate helper out of Parse and export it

internal/foo/foo.go
```go
<<<<<<< SEARCH
func Parse(s string) (Foo, error) {
	return doParse(s)
}
=======
func Parse(s string) (Foo, error) {
	if err := Validate(s); err != nil {
		return Foo{}, err
	}
	return doParse(s)
}

func Validate(s string) error {
	if s == "" {
		return errors.New("empty input")
	}
	return nil
}
>>>>>>> REPLACE
```

internal/foo/foo_test.go
```go
<<<<<<< SEARCH
func TestParse(t *testing.T) {
}
=======
func TestParse(t *testing.T) {
}

func TestValidate(t *testing.T) {
}
>>>>>>> REPLACE
```

Extracted Validate as its own exported function and added a matching test stub.

> Tokens: 2.1k sent, 512 received.
