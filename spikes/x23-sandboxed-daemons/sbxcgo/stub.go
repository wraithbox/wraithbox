//go:build !cgo

package sbxcgo

import "errors"

// Apply is unavailable without cgo.
func Apply(string, []string) error { return errors.New("built without cgo") }
