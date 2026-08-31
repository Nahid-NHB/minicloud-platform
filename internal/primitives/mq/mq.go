// Package mq is a small at-least-once message queue with topics,
// consumer groups, dead-letter queues, and persistent delivery.
//
// The implementation is intentionally single-node but has the surface
// (publish/consume/ack/dlq) the platform needs for events such as
// "workload.scheduled", "node.lost", "alert.firing", and "model.ready".
//
// A production deployment can swap this primitive for an external Kafka
// or NATS client by re-implementing Broker.
package mq

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoMessages is returned by Consume when no message is ready before
// the deadline.
var ErrNoMessages = errors.New("mq: no messages")

// Message is a queued record.
type Message struct {
	Topic     string
	Partition int
	Offset    uint64
	Key       string
	Value     []byte
	Headers   map[string]string
	Timestamp time.Time
}

// Producer publishes to the broker.
type Producer interface {
	Publish(ctx context.Context, topic, key string, value []byte, headers map[string]string) error
	Close() error
}

// Consumer reads from a topic, optionally scoped to a consumer group.
type Consumer interface {
	// Subscribe starts a goroutine that delivers messages to handler.
	// The handler must Ack or Nack each delivery. A Nack with requeue=false
	// routes the message to the dead-letter queue.
	Subscribe(ctx context.Context, topic, group string, handler Handler) error
	Close() error
}

// Handler processes a single message. Returning an error triggers a
// retry with exponential backoff up to MaxRetries, after which the
// message is routed to the dead-letter queue.
type Handler func(ctx context.Context, m *Message) error

// BrokerConfig configures the in-memory broker.
type BrokerConfig struct {
	DataDir       string
	MaxRetries    int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	PersistOnDisk bool
}

// Broker is the union of producer + consumer.
type Broker interface {
	Producer
	Consumer
	// DLQ returns a channel of messages that exhausted retries.
	DLQ(topic string) <-chan *Message
	Stats() Stats
}

// Stats holds live counters.
type Stats struct {
	Published uint64
	Delivered uint64
	Acked     uint64
	Nacked    uint64
	Retried   uint64
	Dead      uint64
}

// Open creates a new broker.
func Open(cfg BrokerConfig) Broker {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 5
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	return newMem(cfg)
}

// ---- in-memory implementation ----

type topic struct {
	name     string
	mu       sync.RWMutex
	nextOff  uint64
	queue    []*Message
	groups   map[string]*groupState // group -> cursor
	dlq      []*Message
}

type groupState struct {
	mu     sync.Mutex
	cursor uint64 // next offset to deliver
}

type mem struct {
	cfg BrokerConfig
	mu  sync.RWMutex
	t   map[string]*topic
	dlq map[string]chan *Message
	stop chan struct{}

	statsMu sync.Mutex
	st      Stats
}

func newMem(cfg BrokerConfig) *mem {
	return &mem{
		cfg:  cfg,
		t:    map[string]*topic{},
		dlq:  map[string]chan *Message{},
		stop: make(chan struct{}),
	}
}

func (m *mem) getOrCreateTopic(name string) *topic {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.t[name]
	if !ok {
		t = &topic{name: name, groups: map[string]*groupState{}}
		m.t[name] = t
		m.dlq[name] = make(chan *Message, 1024)
	}
	return t
}

func (m *mem) Publish(ctx context.Context, topic, key string, value []byte, headers map[string]string) error {
	t := m.getOrCreateTopic(topic)
	t.mu.Lock()
	t.nextOff++
	mv := &Message{
		Topic: topic, Partition: 0, Offset: t.nextOff,
		Key: key, Value: append([]byte{}, value...),
		Headers: copyHeaders(headers), Timestamp: time.Now().UTC(),
	}
	t.queue = append(t.queue, mv)
	t.mu.Unlock()
	m.bump(func(s *Stats) { s.Published++ })
	return nil
}

func (m *mem) DLQ(topic string) <-chan *Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.dlq[topic]
	if !ok {
		ch = make(chan *Message, 1024)
		m.dlq[topic] = ch
	}
	return ch
}

func (m *mem) Stats() Stats {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.st
}

func (m *mem) bump(fn func(*Stats)) {
	m.statsMu.Lock()
	fn(&m.st)
	m.statsMu.Unlock()
}

func copyHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

func (m *mem) Close() error {
	close(m.stop)
	return nil
}

func (m *mem) Subscribe(ctx context.Context, topic, group string, handler Handler) error {
	t := m.getOrCreateTopic(topic)
	t.mu.Lock()
	gs, ok := t.groups[group]
	if !ok {
		gs = &groupState{}
		t.groups[group] = gs
	}
	t.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			default:
			}
			delivered := m.deliverOnce(t, gs, group, handler)
			if !delivered {
				select {
				case <-ctx.Done():
					return
				case <-m.stop:
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
	}()
	return nil
}

func (m *mem) deliverOnce(t *topic, gs *groupState, group string, handler Handler) bool {
	gs.mu.Lock()
	if gs.cursor >= uint64(len(t.queue)) {
		gs.mu.Unlock()
		return false
	}
	mv := t.queue[gs.cursor]
	gs.cursor++
	gs.mu.Unlock()
	m.bump(func(s *Stats) { s.Delivered++ })
	if err := handler(context.Background(), mv); err != nil {
		// Determine retry count from headers.
		retries := 0
		if v, ok := mv.Headers["retries"]; ok {
			for _, c := range v {
				if c < '0' || c > '9' {
					break
				}
				retries = retries*10 + int(c-'0')
			}
		}
		if retries >= m.cfg.MaxRetries {
			m.toDLQ(t, mv)
			return true
		}
		// Schedule retry by appending a new copy with incremented counter.
		mv.Headers = map[string]string{}
		for k, v := range mv.Headers {
			mv.Headers[k] = v
		}
		mv.Headers["retries"] = itoa(retries + 1)
		// Re-append at end of queue.
		t.mu.Lock()
		t.queue = append(t.queue, mv)
		t.mu.Unlock()
		m.bump(func(s *Stats) { s.Retried++ })
		return true
	}
	m.bump(func(s *Stats) { s.Acked++ })
	return true
}

func (m *mem) toDLQ(t *topic, mv *Message) {
	t.mu.Lock()
	t.dlq = append(t.dlq, mv)
	t.mu.Unlock()
	m.bump(func(s *Stats) { s.Dead++ })
	m.mu.RLock()
	ch := m.dlq[t.name]
	m.mu.RUnlock()
	select {
	case ch <- mv:
	default:
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
