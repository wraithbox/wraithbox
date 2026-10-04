// Command wb-guestd is the agent that runs inside the guest VM. See docs/spec/S06-vm-lifecycle.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-guestd"))
	fmt.Fprintln(os.Stderr, "wb-guestd: not implemented yet")
	os.Exit(2)
}
