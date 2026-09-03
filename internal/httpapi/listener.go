package httpapi

import (
	"net"
	"sync"
)

type limitedListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// LimitListener bounds accepted connections, including clients that have not
// finished sending HTTP headers and therefore have not reached middleware yet.
func LimitListener(listener net.Listener, maximum int) net.Listener {
	return &limitedListener{
		Listener: listener,
		slots:    make(chan struct{}, maximum),
		done:     make(chan struct{}),
	}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	connection, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConnection{
		Conn: connection,
		release: func() {
			<-l.slots
		},
	}, nil
}

func (l *limitedListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type limitedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
