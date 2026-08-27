package gateway

import (
	"sync"
	"testing"
	"time"
)

func TestDeliveryDispatcherBufferedDelivery(t *testing.T) {
	got := make(chan Delivery, 4)
	disp := NewDeliveryDispatcher(4, func(d Delivery) {
		got <- d
	})
	disp.Start()
	defer disp.Stop()
	disp.Send(Delivery{ID: "m1", Text: "one"})
	disp.Send(Delivery{ID: "m2", Text: "two"})
	first := <-got
	second := <-got
	if first.ID != "m1" || second.ID != "m2" {
		t.Fatalf("order = %s, %s", first.ID, second.ID)
	}
}

func TestDeliveryDispatcherDropOldestOnOverflow(t *testing.T) {
	block := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(block) }) }
	defer unblock()
	got := make(chan Delivery, 8)
	disp := NewDeliveryDispatcher(2, func(d Delivery) {
		got <- d
		<-block
	})
	disp.Start()
	defer disp.Stop()
	disp.Send(Delivery{ID: "m1"})
	waitFor(t, func() bool { return disp.Delivered() == 1 }, "first handler started")
	disp.Send(Delivery{ID: "m2"})
	disp.Send(Delivery{ID: "m3"})
	disp.Send(Delivery{ID: "m4"})
	time.Sleep(30 * time.Millisecond)
	if disp.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", disp.Dropped())
	}
	unblock()
	waitFor(t, func() bool { return disp.Delivered() == 3 }, "all deliveries handled")
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, (<-got).ID)
	}
	want := []string{"m1", "m3", "m4"}
	for i, w := range want {
		if ids[i] != w {
			t.Fatalf("delivered = %v, want %v", ids, want)
		}
	}
}

func TestDeliveryDispatcherStopDrainsRemaining(t *testing.T) {
	got := make(chan Delivery, 8)
	disp := NewDeliveryDispatcher(4, func(d Delivery) {
		got <- d
	})
	disp.Start()
	disp.Send(Delivery{ID: "m1"})
	disp.Send(Delivery{ID: "m2"})
	disp.Stop()
	if disp.Delivered() != 2 {
		t.Fatalf("delivered = %d, want 2", disp.Delivered())
	}
	if got := <-got; got.ID != "m1" {
		t.Fatalf("first drained = %s", got.ID)
	}
	if got := <-got; got.ID != "m2" {
		t.Fatalf("second drained = %s", got.ID)
	}
}

func TestDeliveryDispatcherSendAfterStop(t *testing.T) {
	disp := NewDeliveryDispatcher(4, nil)
	disp.Start()
	disp.Stop()
	disp.Send(Delivery{ID: "m1"})
	if disp.Delivered() != 0 {
		t.Fatalf("no delivery expected after stop")
	}
}
