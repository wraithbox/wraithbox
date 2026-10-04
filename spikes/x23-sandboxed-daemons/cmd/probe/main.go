// Command probe applies a sandbox profile to itself (or is started under
// sandbox-exec) and reports what still works.
package main

import (
	"flag"
	"fmt"
	"os"

	"runtime"
	"time"

	"x23/probes"
	"x23/sbxapply"
)

func main() {
	mech := flag.String("mech", "none", "none (already sandboxed or unsandboxed), cgo, pure")
	profile := flag.String("profile", "", "SBPL profile file")
	var params sbxapply.Params
	flag.Var(&params, "D", "profile parameter key=value")
	var e probes.Env
	flag.StringVar(&e.TCPAddr, "tcp", "127.0.0.1:1", "loopback TCP listener")
	flag.StringVar(&e.UnixPath, "unix", "", "Unix socket listener")
	flag.StringVar(&e.ReadFile, "readfile", "", "file outside allowed dirs")
	flag.StringVar(&e.WriteDir, "writedir", "", "dir outside allowed dirs")
	flag.StringVar(&e.StateDir, "statedir", "", "allowed state dir")
	flag.StringVar(&e.RemoteAddr, "remote", "1.1.1.1:443", "internet TCP address")
	flag.StringVar(&e.RemoteName, "name", "example.com", "DNS name")
	flag.Parse()

	// Profile is read before sandboxing; the file need not be allowed.
	d, err := sbxapply.Apply(*mech, *profile, params)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		os.Exit(2)
	}
	fmt.Printf("mechanism=%s profile=%s apply=%s\n", *mech, *profile, d)
	all := probes.Runtime()
	all = append(all, probes.Filesystem(e)...)
	all = append(all, probes.Network(e)...)
	all = append(all, probes.Process()...)
	for _, r := range all {
		fmt.Println(r)
	}
	// Go 1.25+ sysmon re-reads the CPU count and updates GOMAXPROCS.
	for t := time.Now(); time.Since(t) < 3*time.Second; { // busy, so sysmon stays awake
	}
	fmt.Printf("after 3s: GOMAXPROCS=%d NumCPU=%d\n", runtime.GOMAXPROCS(0), runtime.NumCPU())
}
