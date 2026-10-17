// Command wb-guestadmin writes and fetches the guests' wbadmin passwords
// in the secret store, for the wb-guestadmin-* entries of wb-launcher. It
// is the main executable of a helper bundle of its own, which holds the
// `wbadmin` access group in a build that a team signs. See
// docs/spec/S15-least-privilege/S15-02-secret-store.md and
// docs/spec/S15-least-privilege/S15-03-access-rules.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-guestadmin"))
	fmt.Fprintln(os.Stderr, "wb-guestadmin: not implemented yet")
	os.Exit(2)
}
