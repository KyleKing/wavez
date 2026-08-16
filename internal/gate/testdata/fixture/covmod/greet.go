package covmod

func Greet(name string) string {
	if name == "" {
		return "hello, stranger"
	}

	return "hello, " + name
}
