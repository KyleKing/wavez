package main

import (
	"fmt"
	"go/ast"
	"os"
	"strings"
)

func runAddField(it *Intent, r *Result) error {
	pkgName, files, _, err := loadPackage(it.PkgDir)
	if err != nil {
		return err
	}
	_ = pkgName

	dot := strings.LastIndex(it.FieldType, ".")
	if dot < 0 {
		return fmt.Errorf("add field: TYPE.FIELD expected, got %q", it.FieldType)
	}
	typeName, fieldName := it.FieldType[:dot], it.FieldType[dot+1:]

	target, structType, found := findStruct(files, typeName)
	if !found {
		return fmt.Errorf("add field: struct %s not found in %s", typeName, it.PkgDir)
	}
	if len(structType.Fields.List) == 0 {
		return fmt.Errorf("add field: struct %s has no fields to insert after", typeName)
	}
	last := structType.Fields.List[len(structType.Fields.List)-1]
	offset := target.fset.Position(last.End()).Offset

	fieldText := buildFieldText(fieldName, it.FieldFType, it.Tag, it.Doc)
	after, err := insertAfter(target.src, offset, fieldText)
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

	warnConstructors(files, typeName, fieldName, r)
	return nil
}

func findStruct(files []pkgFile, typeName string) (pkgFile, *ast.StructType, bool) {
	for _, f := range files {
		for _, d := range f.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					return f, st, true
				}
			}
		}
	}
	return pkgFile{}, nil, false
}

func buildFieldText(name, ftype, tag, doc string) string {
	var b strings.Builder
	if doc != "" {
		b.WriteString("// " + doc + "\n")
	}
	b.WriteString(name + " " + ftype)
	if tag != "" {
		b.WriteString(" `" + tag + "`")
	}
	b.WriteString("\n")
	return b.String()
}

// warnConstructors flags a New<Type> constructor or With<Field> option
// function that a human should check, without editing either.
func warnConstructors(files []pkgFile, typeName, fieldName string, r *Result) {
	newFn := "New" + typeName
	withFn := "With" + fieldName
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			switch fd.Name.Name {
			case newFn:
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"%s exists in %s; it does not initialize %s.%s, review whether it should",
					newFn, f.path, typeName, fieldName))
			case withFn:
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"%s already exists in %s; the new field may belong to that options pattern instead",
					withFn, f.path))
			}
		}
	}
}
