// Command wbctl manages Wraith Box: projects, policy, images, sessions. See docs/spec/005-cli.md.
package main

import (
	"fmt"
	"os"

	"github.com/lsimons/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wbctl"))
	fmt.Fprintln(os.Stderr, "wbctl: not implemented yet")
	os.Exit(2)
}
