package filter

import (
	"bytes"
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/castio"
)

// recorded returns the output of every recording, as it was read from
// the PTY (chunk boundaries kept).
func recorded(b *testing.B) [][]byte {
	paths, _ := filepath.Glob("../recordings/*.cast")
	var chunks [][]byte
	for _, p := range paths {
		_, evs, err := castio.Read(p)
		if err != nil {
			b.Fatal(err)
		}
		for _, e := range evs {
			if e.Kind == "o" {
				chunks = append(chunks, []byte(e.Data))
			}
		}
	}
	if len(chunks) == 0 {
		b.Skip("no recordings")
	}
	return chunks
}

func benchChunks(b *testing.B, chunks [][]byte) {
	var n int64
	for _, c := range chunks {
		n += int64(len(c))
	}
	b.SetBytes(n)
	b.ReportAllocs()
	for b.Loop() {
		f := New(io.Discard)
		for _, c := range chunks {
			f.Write(c)
		}
	}
}

func split(p []byte, size int) [][]byte {
	var out [][]byte
	for len(p) > size {
		out = append(out, p[:size])
		p = p[size:]
	}
	return append(out, p)
}

func BenchmarkRecorded(b *testing.B) { benchChunks(b, recorded(b)) }

func BenchmarkPlainText(b *testing.B) {
	benchChunks(b, split([]byte(strings.Repeat("The quick brown fox jumps over the lazy dog.\r\n", 1<<15)), 32<<10))
}

func BenchmarkDenseSGR(b *testing.B) {
	benchChunks(b, split([]byte(strings.Repeat("\x1b[38;2;215;119;87mab\x1b[39m\x1b[2G", 1<<15)), 32<<10))
}

func BenchmarkDroppedOSC52(b *testing.B) {
	payload := strings.Repeat("A", maxStr-16)
	benchChunks(b, split([]byte(strings.Repeat("\x1b]52;c;"+payload+"\x07", 64)), 32<<10))
}

func BenchmarkRandom(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	p := make([]byte, 1<<20)
	r.Read(p)
	benchChunks(b, split(p, 32<<10))
}

func BenchmarkCopyBaseline(b *testing.B) {
	chunks := split(bytes.Repeat([]byte("x"), 1<<20), 32<<10)
	b.SetBytes(1 << 20)
	for b.Loop() {
		for _, c := range chunks {
			io.Discard.Write(c)
		}
	}
}
