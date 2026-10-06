package packscan

import (
	"bytes"
	"errors"
	"testing"

	"x26/packgen"
)

var lim = Limits{MaxObjects: 1000, MaxObjectSize: 1 << 20, MaxDeltaSize: 1 << 20, MaxTotalResult: 4 << 20}

func small() []byte {
	blob := []byte("hello\n")
	bid := packgen.ObjectID(packgen.Blob, blob)
	tree := packgen.TreeOne("a", bid)
	tid := packgen.ObjectID(packgen.Tree, tree)
	c := packgen.CommitOf(tid, nil, "one")
	return packgen.Pack([]packgen.Entry{
		packgen.Whole(packgen.Blob, blob), packgen.Whole(packgen.Tree, tree), packgen.Whole(packgen.Commit, c),
	})
}

func deltaPack(src, dst uint64) []byte {
	d := packgen.CopyDelta(src, dst)
	return packgen.Pack([]packgen.Entry{
		{Type: packgen.Blob, Size: src, Body: packgen.Zeros(src)},
		{Type: packgen.OfsDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseIdx: 0},
	})
}

func TestScan(t *testing.T) {
	cases := []struct {
		name string
		pack []byte
		rule string // "" ok, "malformed", or a refusal rule
	}{
		{"small", small(), ""},
		{"delta within limit", deltaPack(1000, 900_000), ""},
		{"delta bomb", deltaPack(512<<10, 1<<30), "max-object-size"},
		{"zlib bomb", packgen.Pack([]packgen.Entry{{Type: packgen.Blob, Size: 2 << 20, Body: packgen.Zeros(2 << 20)}}), "max-object-size"},
		{"header lies low", packgen.Pack([]packgen.Entry{{Type: packgen.Blob, Size: 10, Body: packgen.Zeros(5000)}}), "malformed"},
		{"total", packgen.Pack([]packgen.Entry{
			{Type: packgen.Blob, Size: 1 << 20, Body: packgen.Zeros(1 << 20)},
			{Type: packgen.Blob, Size: 1 << 20, Body: packgen.Zeros(1 << 20)},
			{Type: packgen.Blob, Size: 1 << 20, Body: packgen.Zeros(1 << 20)},
			{Type: packgen.Blob, Size: 1 << 20, Body: packgen.Zeros(1 << 20)},
			{Type: packgen.Blob, Size: 1 << 20, Body: packgen.Zeros(1 << 20)},
		}), "max-total-size"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := Scan(bytes.NewReader(c.pack), &out, lim)
			var ref *Refusal
			switch {
			case c.rule == "" && err != nil:
				t.Fatalf("want ok, got %v", err)
			case c.rule == "" && !bytes.Equal(out.Bytes(), c.pack):
				t.Fatalf("forwarded %d of %d bytes", out.Len(), len(c.pack))
			case c.rule == "malformed" && (err == nil || errors.As(err, &ref)):
				t.Fatalf("want malformed, got %v", err)
			case c.rule != "" && c.rule != "malformed" && (!errors.As(err, &ref) || ref.Rule != c.rule):
				t.Fatalf("want %s, got %v", c.rule, err)
			}
			if err != nil && out.Len() >= len(c.pack) {
				t.Fatalf("refused but forwarded the whole pack")
			}
		})
	}
}

func FuzzScan(f *testing.F) {
	f.Add(small())
	f.Add(deltaPack(10, 100))
	f.Add([]byte("PACK\x00\x00\x00\x02\x00\x00\x00\x01"))
	f.Fuzz(func(t *testing.T, b []byte) {
		var out bytes.Buffer
		st, err := Scan(bytes.NewReader(b), &out, lim)
		if err == nil && (st.TotalResult > lim.MaxTotalResult || st.Largest > lim.MaxObjectSize) {
			t.Fatalf("passed over the limit: %+v", st)
		}
		if out.Len() > len(b) {
			t.Fatalf("forwarded more than read")
		}
	})
}
