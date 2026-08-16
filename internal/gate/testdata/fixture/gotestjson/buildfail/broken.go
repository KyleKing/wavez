package buildfail

func Broken() string {
	return 1 + "x"
}
