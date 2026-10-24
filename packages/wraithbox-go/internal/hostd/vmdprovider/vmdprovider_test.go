package vmdprovider_test

import (
	"io"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vmdprovider"
)

func TestNewRefused(t *testing.T) {
	logs := func(string) io.Writer { return io.Discard }
	for name, cfg := range map[string]vmdprovider.Config{
		"relative program": {Program: "wb-vmd", RunDir: "/run", Logs: logs},
		"relative run dir": {Program: "/wb-vmd", RunDir: "run", Logs: logs},
		"no logs":          {Program: "/wb-vmd", RunDir: "/run"},
	} {
		if _, err := vmdprovider.New(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
