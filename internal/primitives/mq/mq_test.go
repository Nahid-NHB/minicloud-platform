package mq

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPublishConsume(t *testing.T) {
	b := Open(BrokerConfig{})
	defer b.Close()
	ctx := context.Background()
	if err := b.Publish(ctx, "workloads", "k1", []byte("v1"), nil); err != nil {
		t.Fatal(err)
	}
	var got *Message
	wg := sync.WaitGroup{}
	wg.Add(1)
	if err := b.Subscribe(ctx, "workloads", "ctrl", func(ctx context.Context, m *Message) error {
		got = m
		wg.Done()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if got == nil || string(got.Value) != "v1" {
		t.Fatalf("got %+v", got)
	}
	if b.Stats().Acked != 1 {
		t.Fatalf("stats=%+v", b.Stats())
	}
}

func TestRetryThenDLQ(t *testing.T) {
	b := Open(BrokerConfig{MaxRetries: 2, BaseBackoff: time.Millisecond})
	defer b.Close()
	ctx := context.Background()
	if err := b.Publish(ctx, "t", "k", []byte("v"), nil); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := b.Subscribe(ctx, "t", "g", func(ctx context.Context, m *Message) error {
		attempts++
		return errors.New("nope")
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("did not DLQ; attempts=%d stats=%+v", attempts, b.Stats())
		default:
		}
		if b.Stats().Dead >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestConsumerGroupsIndependent(t *testing.T) {
	b := Open(BrokerConfig{})
	defer b.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		b.Publish(ctx, "t", "k", []byte("v"), nil)
	}
	var a, b1 int
	var mu sync.Mutex
	done := make(chan struct{}, 2)
	b.Subscribe(ctx, "t", "g1", func(_ context.Context, _ *Message) error {
		mu.Lock(); a++; mu.Unlock()
		if a == 5 { done <- struct{}{} }
		return nil
	})
	b.Subscribe(ctx, "t", "g2", func(_ context.Context, _ *Message) error {
		mu.Lock(); b1++; mu.Unlock()
		if b1 == 5 { done <- struct{}{} }
		return nil
	})
	<-done
	<-done
	if a != 5 || b1 != 5 {
		t.Fatalf("a=%d b=%d", a, b1)
	}
}
