// Command wb-proxyd is the egress proxy: it applies HTTP policy, replaces
// credentials and is the only Wraith Box process that connects upstream.
// See docs/spec/007-egress-gateway.md and docs/spec/009-policy-credentials-audit.md.
package main

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb-proxyd"))
	fmt.Fprintln(os.Stderr, "wb-proxyd: not implemented yet")
	os.Exit(2)
}
