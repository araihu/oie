package xmpp

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/araihu/oie/internal/application"
	"mellium.im/xmpp/jid"
)

type archivePriority uint8

const (
	archiveInteractive archivePriority = iota
	archiveMaintenance
	archiveAutomatic
)

type archiveWaiter struct{ priority archivePriority }

type archiveQueue struct {
	mu            sync.Mutex
	wake          chan struct{}
	waiting       []*archiveWaiter
	active        bool
	nextAutomatic time.Time
}

func (m *Manager) archiveQueue(scope string) (*archiveQueue, error) {
	address, err := jid.Parse(scope)
	if err != nil || address.Domainpart() == "" {
		return nil, application.ErrInvalid
	}
	m.archiveQueuesMu.Lock()
	defer m.archiveQueuesMu.Unlock()
	domain := address.Domainpart()
	if q := m.archiveQueues[domain]; q != nil {
		return q, nil
	}
	if m.archiveQueues == nil {
		m.archiveQueues = make(map[string]*archiveQueue)
	}
	q := &archiveQueue{wake: make(chan struct{})}
	m.archiveQueues[domain] = q
	return q, nil
}

func (q *archiveQueue) notify() {
	close(q.wake)
	q.wake = make(chan struct{})
}

func (q *archiveQueue) acquire(ctx context.Context, priority archivePriority) (func(), error) {
	w := &archiveWaiter{priority: priority}
	q.mu.Lock()
	if len(q.waiting) >= 256 {
		q.mu.Unlock()
		return nil, application.ErrProtocolUnavailable
	}
	q.waiting = append(q.waiting, w)
	q.notify()
	for {
		if err := ctx.Err(); err != nil {
			q.waiting = slices.DeleteFunc(q.waiting, func(waiter *archiveWaiter) bool { return waiter == w })
			q.notify()
			q.mu.Unlock()
			return nil, err
		}
		first := slices.MinFunc(q.waiting, func(a, b *archiveWaiter) int {
			return int(a.priority) - int(b.priority)
		})
		delay := time.Duration(0)
		if !q.active && first == w {
			if priority == archiveAutomatic {
				delay = time.Until(q.nextAutomatic)
			}
			if delay <= 0 {
				q.waiting = slices.DeleteFunc(q.waiting, func(waiter *archiveWaiter) bool { return waiter == w })
				q.active = true
				q.mu.Unlock()
				return func() {
					q.mu.Lock()
					q.active = false
					if priority == archiveAutomatic {
						q.nextAutomatic = time.Now().Add(time.Second)
					}
					q.notify()
					q.mu.Unlock()
				}, nil
			}
		}
		wake := q.wake
		q.mu.Unlock()
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-wake:
			case <-ctx.Done():
			}
			timer.Stop()
		} else {
			select {
			case <-wake:
			case <-ctx.Done():
			}
		}
		q.mu.Lock()
	}
}

func (m *Manager) acquireArchive(ctx context.Context, scope string, priority archivePriority) (func(), error) {
	q, err := m.archiveQueue(scope)
	if err != nil {
		return nil, err
	}
	return q.acquire(ctx, priority)
}
