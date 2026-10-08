// X04-tls-inspection spike: a Go HTTPS client. Throwaway.
//
//	x04-goclient <url> [count] [interval seconds]
//
// Each request uses a new transport (a full TLS handshake) and prints one
// JSON line: time, status or error. Exit 1 if the last request failed.
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	url := os.Args[1]
	n, every := 1, 0
	if len(os.Args) > 3 {
		n, _ = strconv.Atoi(os.Args[2])
		every, _ = strconv.Atoi(os.Args[3])
	}
	ok := false
	for i := 0; i < n; i++ {
		if i > 0 {
			time.Sleep(time.Duration(every) * time.Second)
		}
		c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
		out := map[string]any{"t": time.Now().UTC().Format(time.RFC3339), "i": i}
		resp, err := c.Get(url)
		if err != nil {
			out["err"] = err.Error()
			ok = false
		} else {
			resp.Body.Close()
			out["status"] = resp.StatusCode
			ok = true
		}
		b, _ := json.Marshal(out)
		os.Stdout.Write(append(b, '\n'))
	}
	if !ok {
		os.Exit(1)
	}
}
