// Command wb-credset is the helper that `wb cred set`, `wb cred rm` and
// `wb cred list` run as the user: it writes, lists and deletes the
// binding secrets in the secret store. It is the main executable of a
// helper bundle of its own, which holds the `binding` access group in a
// build that a team signs. See
// docs/spec/S15-least-privilege/S15-02-secret-store.md and
// docs/spec/S10-tech-stack/S10-02-packaging-and-signing.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-credset"))
	fmt.Fprintln(os.Stderr, "wb-credset: not implemented yet")
	os.Exit(2)
}
