package codeintel

// File is one indexed source file.
type File struct {
	Path        string
	ContentHash string
	ID          int64
	MTime       int64
	Size        int64
}

// Symbol is one extracted declaration.
type Symbol struct {
	FilePath  string
	Kind      string
	Name      string
	Signature string
	Doc       string
	ID        int64
	FileID    int64
	StartByte uint
	EndByte   uint
	StartLine int
	EndLine   int
}
