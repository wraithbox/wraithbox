package vmdspawn

import (
	"io"
	"path/filepath"
	"testing"
)

// Start refuses a program that isn't an absolute path, so wb-vmd never
// comes from PATH, and a slot that isn't a slot's name, before it makes
// anything.
func TestConfigRefused(t *testing.T) {
	good := Config{
		Program: filepath.Join(t.TempDir(), "wb-vmd"), Slot: "work", RunDir: t.TempDir(),
		Endpoint: "vmd-work-1", Version: "1.2.3", Log: func(io.Reader) {},
	}
	if err := good.check(); err != nil {
		t.Fatalf("good config: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"relative program": func(c *Config) { c.Program = "wb-vmd" },
		"empty slot":       func(c *Config) { c.Slot = "" },
		"slot with a path": func(c *Config) { c.Slot = "../work" },
		"slot with a '='":  func(c *Config) { c.Slot = "work=x" },
		"no version":       func(c *Config) { c.Version = "" },
		"no log":           func(c *Config) { c.Log = nil },
	} {
		c := good
		change(&c)
		if _, err := Start(t.Context(), c); err == nil {
			t.Errorf("%s: Start succeeded", name)
		}
	}
}
