// Command wb-hostd is the per-user host daemon: sessions, policy, approvals,
// audit and the git gateway, on every host OS. VMs themselves are run by the
// platform's wb-vmd. See docs/spec/S04-architecture.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-hostd"))
	fmt.Fprintln(os.Stderr, "wb-hostd: not implemented yet")
	os.Exit(2)
}
