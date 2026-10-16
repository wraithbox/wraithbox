// Command wb-proxyd is the egress proxy: it applies HTTP policy, replaces
// credentials and makes every upstream connection for the guests.
// wb-download, which fetches what an image build needs, connects to the
// internet as well (docs/spec/S17-install-and-upgrade/S17-00-install-and-upgrade.md).
// See docs/spec/S07-egress-gateway/S07-00-egress-gateway.md and docs/spec/S09-policy-credentials-audit/S09-00-policy-credentials-audit.md.
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
