package unsparse

import (
	"bytes"
	"encoding/binary"
	"errors"
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

var headerSize = binary.Size(Unsparsed{})

func TestHeader(t *testing.T) {
	var packed bytes.Buffer
	if err := Unsparse(&packed, bytes.NewReader([]byte("hi"))); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(packed.Bytes(), Magic[:]) {
		t.Fatalf("output %q doesn't start with magic", packed.Bytes())
	}
	hdr, err := ReadHeader(&packed)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Version != Version || hdr.ApparentSize != 2 {
		t.Errorf("unexpected header %+v", hdr)
	}
}

func TestReadHeaderErrors(t *testing.T) {
	good := Unsparsed{Magic: Magic, Version: Version, ApparentSize: 1}
	enc := func(h Unsparsed) []byte {
		var b bytes.Buffer
		binary.Write(&b, byteOrder, h)
		return b.Bytes()
	}
	badVersion, badFlags, negSize, badMagic := good, good, good, good
	badVersion.Version = 99
	badFlags.Flags = 1
	negSize.ApparentSize = -1
	badMagic.Magic[0] = 0

	for _, tc := range []struct {
		name  string
		input []byte
		magic bool // want ErrNotUnsparsed
	}{
		{"empty", nil, true},
		{"short", enc(good)[:5], true},
		{"bad magic", enc(badMagic), true},
		{"bad version", enc(badVersion), false},
		{"bad flags", enc(badFlags), false},
		{"negative size", enc(negSize), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadHeader(bytes.NewReader(tc.input))
			if err == nil {
				t.Fatal("expected error")
			}
			if got := errors.Is(err, ErrNotUnsparsed); got != tc.magic {
				t.Errorf("errors.Is(ErrNotUnsparsed) = %v, want %v (%v)", got, tc.magic, err)
			}
		})
	}
}
