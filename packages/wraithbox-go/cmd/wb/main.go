// Command wb is the drop-in replacement for claude: it runs Claude Code inside a Wraith Box sandbox. See docs/spec/005-cli.md.
package main

import (
	"fmt"
	"os"

	"github.com/lsimons/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	fmt.Println(version.String("wb"))
	fmt.Fprintln(os.Stderr, "wb: not implemented yet")
	os.Exit(2)
}
