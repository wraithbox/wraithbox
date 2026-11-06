// Command tlsserver is the TLS server TestC02GuestTrustVM runs in the
// guest, on loopback. It presents the leaf of -cert, a PEM file with the
// certificate chain and the PKCS8 key, to every client, and answers each
// request with a fixed body. A second, plain listener takes HTTP CONNECT
// for any host and joins the tunnel to the TLS listener, so a client
// that can't be told an address, such as URLSession, reaches it through
// a proxy. It writes both ports to -ports, and logs each request, each
// tunnel and each handshake error to standard error.
//
// It is in testdata, so `go build ./...` and the linters leave it out.
// The test builds it for darwin/arm64.
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// The bodies of the answers. The test looks for them in each client's
// output. A git client reads refsBody as the repository's refs over the
// dumb HTTP protocol, and headBody as its HEAD.
const (
	okBody   = "wbtest-ok\n"
	refsBody = "0123456789abcdef0123456789abcdef01234567\trefs/heads/main\n"
	headBody = "ref: refs/heads/main\n"
)

func main() {
	certFile := flag.String("cert", "", "PEM file with the leaf's chain and its PKCS8 key")
	portsFile := flag.String("ports", "", "file the TLS and the proxy ports are written to")
	lifetime := flag.Duration("lifetime", 5*time.Minute, "exit after this long")
	flag.Parse()
	if err := run(*certFile, *portsFile, *lifetime); err != nil {
		log.Fatal(err)
	}
}

func run(certFile, portsFile string, lifetime time.Duration) error {
	cert, err := tls.LoadX509KeyPair(certFile, certFile)
	if err != nil {
		return err
	}
	tlsLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tlsAddr := tlsLn.Addr().(*net.TCPAddr)
	proxyAddr := proxyLn.Addr().(*net.TCPAddr)
	ports := fmt.Sprintf("%d %d\n", tlsAddr.Port, proxyAddr.Port)
	if err := os.WriteFile(portsFile+".tmp", []byte(ports), 0o600); err != nil {
		return err
	}
	if err := os.Rename(portsFile+".tmp", portsFile); err != nil {
		return err
	}
	errs := make(chan error, 2)
	go func() {
		srv := &http.Server{Handler: http.HandlerFunc(answer), ReadHeaderTimeout: 10 * time.Second}
		errs <- srv.Serve(tlsLn)
	}()
	go func() {
		srv := &http.Server{Handler: tunnel(tlsAddr.String()), ReadHeaderTimeout: 10 * time.Second}
		errs <- srv.Serve(proxyLn)
	}()
	select {
	case err := <-errs:
		return err
	case <-time.After(lifetime):
		return errors.New("lifetime over")
	}
}

// answer answers every path with okBody, but those a git client asks for
// over the dumb HTTP protocol.
func answer(w http.ResponseWriter, r *http.Request) {
	log.Printf("request %s %s, user agent %q", r.Method, r.URL.Path, r.UserAgent())
	w.Header().Set("Content-Type", "text/plain")
	switch {
	case strings.HasSuffix(r.URL.Path, "/info/refs"):
		_, _ = io.WriteString(w, refsBody)
	case strings.HasSuffix(r.URL.Path, "/HEAD"):
		_, _ = io.WriteString(w, headBody)
	default:
		_, _ = io.WriteString(w, okBody)
	}
}

// tunnel answers a CONNECT for any host with a tunnel to the TLS
// listener at addr, and refuses every other method.
func tunnel(addr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "only CONNECT", http.StatusMethodNotAllowed)
			return
		}
		log.Printf("CONNECT %s", r.Host)
		up, err := net.Dial("tcp", addr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = up.Close()
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		down, buf, err := hj.Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = io.WriteString(down, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() {
			_, _ = io.Copy(up, buf)
			_ = up.Close()
		}()
		_, _ = io.Copy(down, up)
		_ = down.Close()
	}
}
