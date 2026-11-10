package vmdspawn

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Start refuses a program that isn't an absolute path, so wb-vmd never
// comes from PATH, a slot that isn't a slot's name, and a build password
// pipe missing in the build slot or given to another, before it makes
// anything.
func TestConfigRefused(t *testing.T) {
	good := Config{
		Program: filepath.Join(t.TempDir(), "wb-vmd"), Slot: "work", RunDir: t.TempDir(),
		Endpoint: "vmd-work-1", Version: "1.2.3", Log: func(io.Reader) {},
	}
	if err := good.check(); err != nil {
		t.Fatalf("good config: %v", err)
	}
	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pipeRead.Close(), pipeWrite.Close() })
	build := good
	build.Slot, build.ProvisioningPipe = "build", pipeRead
	if err := build.check(); err != nil {
		t.Fatalf("good build config: %v", err)
	}
	for name, change := range map[string]func(*Config){
		// Descriptor 5 is the build slot's alone, and the build slot
		// needs it (S04-03-self-sandboxing, "How `wb-vmd` is started").
		"build without the password pipe": func(c *Config) { c.Slot = "build" },
		"password pipe in the work slot":  func(c *Config) { c.ProvisioningPipe = pipeRead },
		"password pipe in the layer slot": func(c *Config) { c.Slot, c.ProvisioningPipe = "layer", pipeRead },
		"relative program":                func(c *Config) { c.Program = "wb-vmd" },
		"empty slot":                      func(c *Config) { c.Slot = "" },
		"slot with a path":                func(c *Config) { c.Slot = "../work" },
		"slot with a '='":                 func(c *Config) { c.Slot = "work=x" },
		"no version":                      func(c *Config) { c.Version = "" },
		"no log":                          func(c *Config) { c.Log = nil },
	} {
		c := good
		change(&c)
		if _, err := Start(t.Context(), c); err == nil {
			t.Errorf("%s: Start succeeded", name)
		}
	}
}
