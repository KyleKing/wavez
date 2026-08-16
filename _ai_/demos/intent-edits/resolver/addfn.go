package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runAddFn(it *Intent, r *Result) error {
	pkgName, files, testFiles, err := loadPackage(it.PkgDir)
	if err != nil {
		return err
	}

	var target pkgFile
	var offset int
	var near string
	if it.Near != "" {
		f, off, _, ok := findSymbolFile(files, it.Near)
		if !ok {
			r.Warnings = append(r.Warnings, fmt.Sprintf("near symbol %q not found, appending to package's main file instead", it.Near))
			target = mainFile(pkgName, files)
			offset = len(target.src)
		} else {
			target, offset, near = f, off, it.Near
		}
	} else {
		target = mainFile(pkgName, files)
		offset = len(target.src)
	}

	fnText := buildFnDecl(it, files)

	var after string
	if near != "" {
		after, err = insertAfter(target.src, offset, fnText)
	} else {
		after, err = appendAtEOF(target.src, fnText)
	}
	if err != nil {
		return err
	}

	after, added, err := runImports(target.path, target.src, after)
	if err != nil {
		return err
	}
	r.ImportsAdded = append(r.ImportsAdded, added...)
	r.record(target.path, target.src, after)
	if err := os.WriteFile(target.path, []byte(after), 0o644); err != nil {
		return err
	}

	if it.Test {
		if err := addTestStub(it, files, testFiles, r); err != nil {
			return err
		}
	}
	return nil
}

func buildFnDecl(it *Intent, files []pkgFile) string {
	doc := buildDocComment(it.FnName, it.Doc, docStyle(files))
	body := fmt.Sprintf("\t// HOLE: implement %s\n\tpanic(\"TODO(intent): %s\")", it.FnName, it.FnName)
	return fmt.Sprintf("%sfunc %s%s {\n%s\n}\n", doc, it.FnName, it.FnSig, body)
}

func buildDocComment(name, doc string, named bool) string {
	if doc == "" {
		if !named {
			return ""
		}
		return fmt.Sprintf("// %s is not yet implemented (see HOLE below).\n", name)
	}
	text := doc
	if !strings.HasPrefix(text, name+" ") {
		text = name + " " + text
	}
	return "// " + text + "\n"
}

// addTestStub adds a TestNAME to the sibling *_test.go for the file that
// holds `near`, mirroring that file's t.Parallel and table-driven habits.
func addTestStub(it *Intent, files []pkgFile, testFiles []string, r *Result) error {
	if len(testFiles) == 0 {
		r.Warnings = append(r.Warnings, "test=yes requested but no *_test.go file exists in "+it.PkgDir+"; skipped")
		return nil
	}

	testPath := testFiles[0]
	if it.Near != "" {
		if f, _, _, ok := findSymbolFile(files, it.Near); ok {
			base := strings.TrimSuffix(filepath.Base(f.path), ".go")
			candidate := filepath.Join(filepath.Dir(f.path), base+"_test.go")
			for _, tf := range testFiles {
				if tf == candidate {
					testPath = tf
				}
			}
		}
	}

	before, err := os.ReadFile(testPath)
	if err != nil {
		return err
	}
	src := string(before)
	parallel := strings.Contains(src, "t.Parallel()")
	tableDriven := strings.Contains(src, "for _, tt") || strings.Contains(src, "tests :=") || strings.Contains(src, "tests := []struct")

	stub := buildTestStub(it.FnName, it.Near, parallel, tableDriven)
	after, err := appendAtEOF(src, stub)
	if err != nil {
		return err
	}
	after, added, err := runImports(testPath, src, after)
	if err != nil {
		return err
	}
	r.ImportsAdded = append(r.ImportsAdded, added...)
	r.record(testPath, src, after)
	return os.WriteFile(testPath, []byte(after), 0o644)
}

func buildTestStub(name, near string, parallel, tableDriven bool) string {
	mirrorOf := near
	if mirrorOf == "" {
		mirrorOf = name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n", name)
	if parallel {
		b.WriteString("\tt.Parallel()\n\n")
	}
	if tableDriven {
		b.WriteString("\ttests := []struct {\n\t\tname string\n\t}{\n\t\t// HOLE: table rows mirroring Test" + mirrorOf + "\n\t}\n\n")
		b.WriteString("\tfor _, tt := range tests {\n\t\tt.Run(tt.name, func(t *testing.T) {\n")
		if parallel {
			b.WriteString("\t\t\tt.Parallel()\n\n")
		}
		b.WriteString("\t\t\t// HOLE: exercise " + name + ", mirroring Test" + mirrorOf + "\n\t\t})\n\t}\n")
	} else {
		b.WriteString("\t// HOLE: exercise " + name + ", mirroring Test" + mirrorOf + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}
