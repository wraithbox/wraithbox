// Command wb-netd is the guest-facing network service: it runs the userspace
// TCP/IP stack and DNS for a VM's virtual NIC and hands accepted streams to
// wb-proxyd. It holds no secrets and makes no outbound connections.
// See docs/spec/007-egress-gateway.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-netd"))
	fmt.Fprintln(os.Stderr, "wb-netd: not implemented yet")
	os.Exit(2)
}
