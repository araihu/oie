package xmpp

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/ping"
	"mellium.im/xmpp/stanza"
)

const connectionIdle = 25 * time.Second
const pingTimeout = 10 * time.Second

type activityConn struct {
	net.Conn
	mu       sync.Mutex
	lastRead time.Time
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.lastRead = time.Now()
		c.mu.Unlock()
	}
	return n, err
}

func (c *activityConn) idleFor() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastRead)
}

func watchConnection(ctx context.Context, session *mx.Session, conn *activityConn) {
	idle := connectionIdle + rand.N(5*time.Second)
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if remaining := idle - conn.idleFor(); remaining > 0 {
			timer.Reset(remaining)
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, pingTimeout)
		// Closing this transport also releases a ping waiting for the session writer.
		stop := context.AfterFunc(attempt, func() { _ = conn.Close() })
		err := ping.Send(attempt, session, jid.JID{})
		stop()
		cancel()
		var response stanza.Error
		if err != nil && !errors.As(err, &response) {
			_ = conn.Close()
			return
		}
		timer.Reset(idle)
	}
}
