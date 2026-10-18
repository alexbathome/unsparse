package unsparse

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const testSize = 16 << 20

func create(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func makeSparse(t *testing.T) (*os.File, []byte) {
	t.Helper()
	f := create(t, "sparse")

	want := make([]byte, testSize)
	for _, off := range []int64{0, 3 << 20, 9<<20 + 123} {
		data := bytes.Repeat([]byte("hello sparse "), 1000)
		copy(want[off:], data)
		if _, err := f.WriteAt(data, off); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Truncate(testSize); err != nil {
		t.Fatal(err)
	}
	return f, want
}

// roundTrip packs src, unpacks it into a new file, and checks it matches want.
// It returns the packed size.
func roundTrip(t *testing.T, src SparseReader, want []byte) int {
	t.Helper()
	var packed bytes.Buffer
	if err := Unsparse(&packed, src); err != nil {
		t.Fatal(err)
	}
	n := packed.Len()

	dst := create(t, "out")
	if err := Sparse(dst, &packed); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(want))
	}
	return n
}

func TestRoundTrip(t *testing.T) {
	src, want := makeSparse(t)
	holes := fsKeepsHoles(src, testSize)

	for _, tc := range []struct {
		name     string
		lo, hi   int64
		dataSize int // bytes of data in want[lo:hi]
	}{
		{"whole", 0, testSize, 3 * 13000},
		{"offset", 2 << 20, 12 << 20, 2 * 13000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := roundTrip(t, io.NewSectionReader(src, tc.lo, tc.hi-tc.lo), want[tc.lo:tc.hi])
			if !holes {
				t.Logf("filesystem reports no holes; packed size %d", n)
			} else if n > 2*tc.dataSize+64<<10 {
				t.Errorf("packed size %d, expected holes to be omitted", n)
			}
		})
	}
}

func TestRoundTripBytes(t *testing.T) {
	_, want := makeSparse(t)
	n := roundTrip(t, bytes.NewReader(want), want)
	if n < testSize {
		t.Errorf("packed size %d, expected a single extent", n)
	}
}

func TestTrailingHole(t *testing.T) {
	src := create(t, "empty")
	if err := src.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, io.NewSectionReader(src, 0, 1<<20), make([]byte, 1<<20))
}
