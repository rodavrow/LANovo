package network

import (
	"sync"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/network/policy"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type memory struct{ c schema.Network }

func (m *memory) Read() schema.Network { return m.c }

func (m *memory) Update(change func(*schema.Network)) error {
	change(&m.c)
	return nil
}

type blocks struct {
	mu   sync.Mutex
	got  []bool
	done chan struct{}
}

func (b *blocks) block(on bool) error {
	b.mu.Lock()
	b.got = append(b.got, on)
	b.mu.Unlock()
	b.done <- struct{}{}
	return nil
}

func TestTheBlockFollowsLocalOnly(t *testing.T) {
	b := &blocks{done: make(chan struct{}, 4)}
	n := newNetwork(policy.New(&memory{c: schema.DefaultNetwork()}), b.block)

	for _, step := range []struct {
		value string
		block *bool
	}{
		{"quiet", nil},
		{"local", ptr(true)},
		{"local", nil},
		{"quiet", ptr(false)},
		{"open", nil},
	} {
		if err := n.Settings().Change("internet", step.value); err != nil {
			t.Fatalf("Change(internet, %s) = %v", step.value, err)
		}
		if step.block == nil {
			continue
		}
		<-b.done
		b.mu.Lock()
		last := b.got[len(b.got)-1]
		b.mu.Unlock()
		if last != *step.block {
			t.Errorf("after %s the block went %v, want %v", step.value, last, *step.block)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.got) != 2 {
		t.Errorf("the block changed %d times, want 2 (into and out of local): %v", len(b.got), b.got)
	}
}

func ptr(v bool) *bool { return &v }
