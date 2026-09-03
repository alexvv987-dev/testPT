package httpapi

import (
	"net"
	"testing"
	"time"
)

func TestLimitListenerWaitsUntilConnectionCloses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	limited := LimitListener(listener, 1)
	t.Cleanup(func() { _ = limited.Close() })

	firstClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial first connection: %v", err)
	}
	t.Cleanup(func() { _ = firstClient.Close() })
	firstServer, err := limited.Accept()
	if err != nil {
		t.Fatalf("accept first connection: %v", err)
	}
	t.Cleanup(func() { _ = firstServer.Close() })

	secondClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial second connection: %v", err)
	}
	t.Cleanup(func() { _ = secondClient.Close() })
	accepted := make(chan net.Conn, 1)
	errorsChannel := make(chan error, 1)
	go func() {
		connection, acceptErr := limited.Accept()
		accepted <- connection
		errorsChannel <- acceptErr
	}()

	select {
	case <-accepted:
		t.Fatal("second connection was accepted before the first closed")
	case <-time.After(50 * time.Millisecond):
	}
	if err := firstServer.Close(); err != nil {
		t.Fatalf("close first connection: %v", err)
	}

	select {
	case secondServer := <-accepted:
		if acceptErr := <-errorsChannel; acceptErr != nil {
			t.Fatalf("accept second connection: %v", acceptErr)
		}
		if err := secondServer.Close(); err != nil {
			t.Fatalf("close second connection: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second connection was not accepted after the first closed")
	}
}
