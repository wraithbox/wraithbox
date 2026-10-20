package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// tcpPair returns both ends of a loopback TCP connection, which can end
// its writes alone on every OS.
func tcpPair(t *testing.T) (a, b net.Conn) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	var dialer net.Dialer
	a, err = dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b = <-accepted
	if b == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

func closeWrite(t *testing.T, c net.Conn) {
	t.Helper()
	hc, ok := c.(halfCloser)
	if !ok {
		t.Fatalf("a %T can't end its writes alone", c)
	}
	if err := hc.CloseWrite(); err != nil {
		t.Fatal(err)
	}
}

// TestRelayEndsOnCancel: a relay with both peers still open ends when
// ctx does, as on Ctrl-C.
func TestRelayEndsOnCancel(t *testing.T) {
	_, clientSide := tcpPair(t)
	guestSide, _ := tcpPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		relayClient(ctx, slog.New(slog.DiscardHandler), clientSide,
			func(context.Context) (net.Conn, error) { return guestSide, nil })
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay didn't end when its context did")
	}
}

// TestRelay: bytes pass both ways unchanged, and a half-close from the
// client reaches the guest while the guest can still answer.
func TestRelay(t *testing.T) {
	client, clientSide := tcpPair(t)
	guestSide, guest := tcpPair(t)
	done := make(chan struct{})
	go func() {
		relay(clientSide, guestSide)
		close(done)
	}()

	request := []byte("request \x00\xff bytes")
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	closeWrite(t, client)
	_ = guest.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(guest)
	if err != nil || string(got) != string(request) {
		t.Fatalf("guest read %q, %v; want %q then end of file", got, err, request)
	}
	if _, err := guest.Write([]byte("answer")); err != nil {
		t.Fatal(err)
	}
	closeWrite(t, guest)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err = io.ReadAll(client)
	if err != nil || string(got) != "answer" {
		t.Fatalf("client read %q, %v", got, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay didn't end after both sides ended their writes")
	}
}
