package main

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	start, end, err := parseRange("30000-30199")
	if err != nil || start != 30000 || end != 30199 {
		t.Fatalf("parseRange() = %d, %d, %v", start, end, err)
	}
	for _, value := range []string{"", "30000", "1023-2000", "40000-30000", "30000-70000"} {
		if _, _, err := parseRange(value); err == nil {
			t.Fatalf("parseRange(%q) error = nil", value)
		}
	}
}

func TestPipePreservesClientHalfClose(t *testing.T) {
	backendListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendListener.Close()
	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		connection, acceptErr := backendListener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		request, _ := io.ReadAll(connection)
		_, _ = connection.Write(append([]byte("reply:"), request...))
	}()

	proxyListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()
	proxyDone := make(chan struct{})
	go func() {
		defer close(proxyDone)
		clientSide, acceptErr := proxyListener.Accept()
		if acceptErr != nil {
			return
		}
		backendSide, dialErr := net.Dial("tcp", backendListener.Addr().String())
		if dialErr != nil {
			_ = clientSide.Close()
			return
		}
		pipe(clientSide, backendSide)
	}()

	connection, err := net.DialTimeout("tcp", proxyListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tcpConnection := connection.(*net.TCPConn)
	if _, err := tcpConnection.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tcpConnection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(tcpConnection)
	if err != nil {
		t.Fatal(err)
	}
	_ = tcpConnection.Close()
	if string(reply) != "reply:hello" {
		t.Fatalf("reply = %q, want %q", reply, "reply:hello")
	}

	select {
	case <-backendDone:
	case <-time.After(time.Second):
		t.Fatal("backend did not finish")
	}
	select {
	case <-proxyDone:
	case <-time.After(time.Second):
		t.Fatal("proxy did not finish")
	}
}
