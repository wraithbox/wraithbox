package vmdspawn

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Start refuses a program that isn't an absolute path, so wb-vmd never
// comes from PATH, a slot that isn't a slot's name, and a build password
// pipe missing in the build slot, given to another, or not a pipe,
// before it makes anything. Start closes the pipe it was given, refused
// or not.
func TestConfigRefused(t *testing.T) {
	good := Config{
		Program: filepath.Join(t.TempDir(), "wb-vmd"), Slot: "work", RunDir: t.TempDir(),
		Endpoint: "vmd-work-1", Version: "1.2.3", Log: func(io.Reader) {},
	}
	if err := good.check(); err != nil {
		t.Fatalf("good config: %v", err)
	}
	build := good
	build.Slot, build.ProvisioningPipe = "build", newPipe(t)
	if err := build.check(); err != nil {
		t.Fatalf("good build config: %v", err)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "password"))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		// Descriptor 5 is the build slot's alone, and the build slot
		// needs it (S04-03-self-sandboxing, "How `wb-vmd` is started").
		"build without the password pipe": func(c *Config) { c.Slot = "build" },
		"password pipe in the work slot":  func(c *Config) { c.ProvisioningPipe = newPipe(t) },
		"password pipe in the layer slot": func(c *Config) { c.Slot, c.ProvisioningPipe = "layer", newPipe(t) },
		"password file, not a pipe":       func(c *Config) { c.Slot, c.ProvisioningPipe = "build", file },
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
		if c.ProvisioningPipe != nil {
			if _, err := c.ProvisioningPipe.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("%s: Start left the password descriptor open", name)
			}
		}
	}
}

// newPipe is the read end of a new pipe. The test closes the write end.
func newPipe(t *testing.T) *os.File {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = read.Close(), write.Close() })
	return read
}
