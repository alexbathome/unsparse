package unsparse

import (
	"bytes"
	"io"
	"math/rand/v2"
	"testing"
)

// TestBufferRandom checks Buffer against a dense reference using random
// writes and truncates.
func TestBufferRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var b Buffer
	var ref []byte

	for range 2000 {
		if rng.IntN(10) == 0 {
			n := rng.IntN(1 << 16)
			if err := b.Truncate(int64(n)); err != nil {
				t.Fatal(err)
			}
			if n <= len(ref) {
				ref = ref[:n]
			} else {
				ref = append(ref, make([]byte, n-len(ref))...)
			}
		} else {
			off := rng.IntN(1 << 16)
			p := make([]byte, rng.IntN(512)+1)
			for i := range p {
				p[i] = byte(rng.IntN(255) + 1)
			}
			if _, err := b.WriteAt(p, int64(off)); err != nil {
				t.Fatal(err)
			}
			if end := off + len(p); end > len(ref) {
				ref = append(ref, make([]byte, end-len(ref))...)
			}
			copy(ref[off:], p)
		}

		if b.Size() != int64(len(ref)) {
			t.Fatalf("size %d, want %d", b.Size(), len(ref))
		}
		for k := 1; k < len(b.extents); k++ {
			if b.extents[k-1].end() >= b.extents[k].off {
				t.Fatalf("extents %d and %d overlap or touch", k-1, k)
			}
		}
	}

	got := make([]byte, b.Size())
	if _, err := b.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ref) {
		t.Fatal("contents mismatch")
	}
}

func TestBufferReadAt(t *testing.T) {
	var b Buffer
	b.WriteAt([]byte("abc"), 2)
	b.Truncate(8)

	p := make([]byte, 4)
	n, err := b.ReadAt(p, 1)
	if n != 4 || err != nil || string(p) != "\x00abc" {
		t.Fatalf("ReadAt(1) = %d, %v, %q", n, err, p)
	}
	n, err = b.ReadAt(p, 6)
	if n != 2 || err != io.EOF || string(p[:n]) != "\x00\x00" {
		t.Fatalf("ReadAt(6) = %d, %v, %q", n, err, p[:n])
	}
	if _, err := b.ReadAt(p, 8); err != io.EOF {
		t.Fatalf("ReadAt(8) err = %v, want EOF", err)
	}
}

func TestBufferTruncateFrees(t *testing.T) {
	var b Buffer
	b.WriteAt(bytes.Repeat([]byte{1}, 1<<20), 0)
	if err := b.Truncate(10); err != nil {
		t.Fatal(err)
	}
	if c := cap(b.extents[0].data); c >= 1<<19 {
		t.Errorf("cap after truncate = %d, want discarded tail freed", c)
	}

	got := make([]byte, 20)
	n, _ := b.ReadAt(got, 0)
	if n != 10 || !bytes.Equal(got[:n], bytes.Repeat([]byte{1}, 10)) {
		t.Fatalf("ReadAt after truncate = %d, %v", n, got[:n])
	}
}

func TestNewBuffer(t *testing.T) {
	b := NewBuffer(1 << 40)
	if b.Size() != 1<<40 || len(b.extents) != 0 {
		t.Fatalf("size %d, %d extents; want 1TiB hole", b.Size(), len(b.extents))
	}
	if _, _, err := b.NextData(0); err != io.EOF {
		t.Fatalf("NextData on hole: err = %v, want EOF", err)
	}

	b.WriteAt([]byte("data"), 1024)
	start, end, err := b.NextData(0)
	if start != 1024 || end != 1028 || err != nil {
		t.Fatalf("NextData = %d, %d, %v; want 1024, 1028, nil", start, end, err)
	}
	if b.Size() != 1<<40 {
		t.Fatalf("write inside hole changed size to %d", b.Size())
	}

	defer func() {
		if recover() == nil {
			t.Fatal("NewBuffer(-1) did not panic")
		}
	}()
	NewBuffer(-1)
}

func TestBufferNextData(t *testing.T) {
	var b Buffer
	b.WriteAt([]byte("aa"), 10)
	b.WriteAt([]byte("bb"), 12) // touches, so merges with the first
	b.WriteAt([]byte("cc"), 20)
	b.Truncate(100)

	for _, tc := range []struct {
		off, start, end int64
		err             error
	}{
		{0, 10, 14, nil},
		{11, 11, 14, nil},
		{14, 20, 22, nil},
		{22, 0, 0, io.EOF},
	} {
		start, end, err := b.NextData(tc.off)
		if start != tc.start || end != tc.end || err != tc.err {
			t.Errorf("NextData(%d) = %d, %d, %v; want %d, %d, %v", tc.off, start, end, err, tc.start, tc.end, tc.err)
		}
	}
}

func TestBufferDocExample(t *testing.T) {
	b := NewBuffer(1 << 40)
	b.WriteAt([]byte("hello"), 0)
	b.WriteAt([]byte("world"), 1<<39)

	var packed bytes.Buffer
	if err := Unsparse(&packed, b); err != nil {
		t.Fatal(err)
	}
	if want := headerSize + 2*(16+5); packed.Len() != want {
		t.Errorf("packed size %d, want %d", packed.Len(), want)
	}

	restored := NewBuffer(0)
	if err := Sparse(restored, &packed); err != nil {
		t.Fatal(err)
	}
	if restored.Size() != 1<<40 || len(restored.extents) != 2 {
		t.Fatalf("restored size %d with %d extents", restored.Size(), len(restored.extents))
	}
	p := make([]byte, 5)
	restored.ReadAt(p, 1<<39)
	if string(p) != "world" {
		t.Fatalf("read %q at 512GiB, want world", p)
	}
}

func TestBufferRoundTrip(t *testing.T) {
	src := &Buffer{}
	src.WriteAt([]byte("hello"), 1<<20)
	src.WriteAt([]byte("world"), 5<<20)
	src.Truncate(8 << 20)

	for _, tc := range []struct {
		name   string
		src    SparseReader
		packed int
	}{
		// header + 2 * (extent header + 5 bytes)
		{"buffer", src, headerSize + 2*(16+5)},
		// "llo" and "world"
		{"section", io.NewSectionReader(src, 1<<20+2, 5<<20), headerSize + (16 + 3) + (16 + 5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var packed bytes.Buffer
			if err := Unsparse(&packed, tc.src); err != nil {
				t.Fatal(err)
			}
			if packed.Len() != tc.packed {
				t.Errorf("packed size %d, want %d", packed.Len(), tc.packed)
			}

			var dst Buffer
			if err := Sparse(&dst, &packed); err != nil {
				t.Fatal(err)
			}
			want := make([]byte, tc.src.Size())
			tc.src.ReadAt(want, 0)
			got := make([]byte, dst.Size())
			dst.ReadAt(got, 0)
			if !bytes.Equal(got, want) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}
