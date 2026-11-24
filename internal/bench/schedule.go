package bench

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// Schedule orders the runs of an interleaved invocation: reps repetitions,
// each running every arm once in an order shuffled afresh. Running arms in
// blocks instead would give the later block the earlier one's warm prompt
// cache and whatever else changed on the machine in between, and call it
// an effect of the arm. The same seed gives the same schedule.
func Schedule(arms []Arm, reps int, seed uint64) [][]Arm {
	// The shuffle decides an order, not a secret, and must repeat for a seed.
	rng := mathrand.New(mathrand.NewPCG(seed, seed)) //nolint:gosec // see above

	out := make([][]Arm, 0, reps)

	for range reps {
		order := slices.Clone(arms)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		out = append(out, order)
	}

	return out
}

// NewSeed picks a seed for Schedule. It fits in 32 bits, so that it
// survives a tool that reads the records file's numbers as floats, and is
// never zero, which a record reads as no seed.
func NewSeed() (uint64, error) {
	var b [4]byte

	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("picking a seed: %w", err)
		}

		if seed := binary.LittleEndian.Uint32(b[:]); seed != 0 {
			return uint64(seed), nil
		}
	}
}

// digestLength is how many hex digits of a binary's hash identify a build.
const digestLength = 12

// BinaryDigest identifies a build by the content of its binary. The
// version a binary prints cannot: every local build prints dev, and what
// the toolchain stamps from version control says only "modified" for each
// of the uncommitted trees a change is benchmarked from.
func BinaryDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the owner names the binary to run
	if err != nil {
		return "", fmt.Errorf("opening the wavez binary: %w", err)
	}

	sum := sha256.New()

	_, err = io.Copy(sum, f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return "", fmt.Errorf("hashing the wavez binary: %w", err)
	}

	return hex.EncodeToString(sum.Sum(nil))[:digestLength], nil
}

// TreeDigest identifies a directory by the names and content of its files,
// so runs given different overlays are never pooled.
func TreeDigest(dir string) (string, error) {
	sum := sha256.New()

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("naming %s: %w", path, err)
		}

		body, err := os.ReadFile(path) //nolint:gosec // the owner names the overlay
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		// A hash's Write never fails.
		sum.Write([]byte(filepath.ToSlash(rel) + "\x00" + strconv.Itoa(len(body)) + "\x00"))
		sum.Write(body)

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hashing the overlay: %w", err)
	}

	return hex.EncodeToString(sum.Sum(nil))[:digestLength], nil
}
