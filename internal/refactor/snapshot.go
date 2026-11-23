package refactor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const newFileMode fs.FileMode = 0o644

type savedFile struct {
	data    []byte
	mode    fs.FileMode
	existed bool
}

// snapshot holds each file as it was before an operation touched it, so a
// failure at any later step can put the tree back.
type snapshot struct {
	fsys *os.Root
	kept map[string]savedFile
}

// keep records a file's present state. Only the first call for a path
// counts, because later ones would record the operation's own writes.
func (s *snapshot) keep(rel string) error {
	if _, ok := s.kept[rel]; ok {
		return nil
	}

	name := filepath.FromSlash(rel)

	info, err := s.fsys.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		s.kept[rel] = savedFile{}

		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	data, err := s.fsys.ReadFile(name)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	s.kept[rel] = savedFile{data: data, mode: info.Mode().Perm(), existed: true}

	return nil
}

// write replaces a file's content, keeping its mode and recording what it
// held before.
func (s *snapshot) write(rel string, data []byte) error {
	if err := s.keep(rel); err != nil {
		return err
	}

	mode := newFileMode
	if saved := s.kept[rel]; saved.existed {
		mode = saved.mode
	}

	if err := s.fsys.WriteFile(filepath.FromSlash(rel), data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}

	return nil
}

func (s *snapshot) remove(rel string) error {
	if err := s.keep(rel); err != nil {
		return err
	}

	if err := s.fsys.Remove(filepath.FromSlash(rel)); err != nil {
		return fmt.Errorf("removing %s: %w", rel, err)
	}

	return nil
}

// restore puts every kept file back as it was, writing only those that
// differ. It keeps going past a failure so one unwritable file does not
// strand the rest.
func (s *snapshot) restore() error {
	var errs []error

	for rel, saved := range s.kept {
		if err := s.restoreOne(rel, saved); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (s *snapshot) restoreOne(rel string, saved savedFile) error {
	name := filepath.FromSlash(rel)
	current, err := s.fsys.ReadFile(name)

	if !saved.existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err := s.fsys.Remove(name); err != nil {
			return fmt.Errorf("restoring %s: %w", rel, err)
		}

		return nil
	}

	if err == nil && bytes.Equal(current, saved.data) {
		return nil
	}

	if err := s.fsys.WriteFile(name, saved.data, saved.mode); err != nil {
		return fmt.Errorf("restoring %s: %w", rel, err)
	}

	return nil
}

// original is the content a kept file had before the operation.
func (s *snapshot) original(rel string) ([]byte, bool) {
	saved, ok := s.kept[rel]

	return saved.data, ok && saved.existed
}

// before is original for a caller that reads through an error.
func (s *snapshot) before(rel string) ([]byte, error) {
	data, ok := s.original(rel)
	if !ok {
		return nil, fs.ErrNotExist
	}

	return data, nil
}

// capture reads the present state of every kept file the operation
// changed, so the operation can be put back after a restore.
func (s *snapshot) capture() (map[string]savedFile, error) {
	out := map[string]savedFile{}

	for rel, saved := range s.kept {
		data, err := s.fsys.ReadFile(filepath.FromSlash(rel))
		if errors.Is(err, fs.ErrNotExist) {
			if saved.existed {
				out[rel] = savedFile{}
			}

			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}

		if saved.existed && bytes.Equal(data, saved.data) {
			continue
		}

		mode := newFileMode
		if saved.existed {
			mode = saved.mode
		}

		out[rel] = savedFile{data: data, mode: mode, existed: true}
	}

	return out, nil
}

// apply writes a captured state back over the tree.
func (s *snapshot) apply(state map[string]savedFile) error {
	for rel, file := range state {
		name := filepath.FromSlash(rel)

		if !file.existed {
			if err := s.fsys.Remove(name); err != nil {
				return fmt.Errorf("removing %s: %w", rel, err)
			}

			continue
		}

		if err := s.fsys.WriteFile(name, file.data, file.mode); err != nil {
			return fmt.Errorf("writing %s: %w", rel, err)
		}
	}

	return nil
}
