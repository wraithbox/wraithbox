// x8mount runs wb-guestd's own data mount (internal/guestd.DataMount)
// in a test guest, as root, without wb-hostd: Mount with the volume UUID
// the host recorded, then the check before a session, and prints what
// diskutil reports for the volume. Throwaway spike code for I228.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

func main() {
	uuid := flag.String("uuid", "", "volume UUID the host recorded")
	first := flag.Bool("first", false, "the host still asks for the first mount (claim the root)")
	infoOnly := flag.Bool("info", false, "only print diskutil's report of the volume")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	sys := &guestmount.System{Log: log}
	ctx := context.Background()
	vol, found, err := sys.Info(ctx, *uuid)
	out, _ := json.Marshal(vol)
	fmt.Printf("info: found=%v err=%v volume=%s\n", found, err, out)
	if *infoOnly {
		return
	}
	d := guestd.NewDataMount(sys, nil, log)
	start := time.Now()
	r := d.Mount(ctx, *uuid, *first)
	fmt.Printf("mount: took=%s refusal=%v\n", time.Since(start).Round(time.Millisecond), r)
	start = time.Now()
	r = d.Check(ctx)
	fmt.Printf("check: took=%s refusal=%v\n", time.Since(start).Round(time.Millisecond), r)
	m, err := sys.Statfs(guestd.HomePath)
	mo, _ := json.Marshal(m)
	fmt.Printf("statfs: %s err=%v\n", mo, err)
	if r != nil {
		os.Exit(3)
	}
}
