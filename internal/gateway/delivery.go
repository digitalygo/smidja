package gateway

import (
	"sync"
	"sync/atomic"
)

type DeliveryDispatcher struct {
	mu        sync.RWMutex
	ch        chan Delivery
	handler   func(Delivery)
	wg        sync.WaitGroup
	stopped   bool
	dropped   atomic.Int64
	delivered atomic.Int64
}

func NewDeliveryDispatcher(buffer int, handler func(Delivery)) *DeliveryDispatcher {
	if buffer <= 0 {
		buffer = 64
	}
	return &DeliveryDispatcher{ch: make(chan Delivery, buffer), handler: handler}
}

func (d *DeliveryDispatcher) Start() {
	d.wg.Add(1)
	go d.run()
}

func (d *DeliveryDispatcher) run() {
	defer d.wg.Done()
	for dlv := range d.ch {
		d.delivered.Add(1)
		if d.handler != nil {
			d.handler(dlv)
		}
	}
}

func (d *DeliveryDispatcher) Send(dlv Delivery) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.stopped {
		return
	}
	select {
	case d.ch <- dlv:
		return
	default:
	}
	select {
	case <-d.ch:
		d.dropped.Add(1)
	default:
	}
	select {
	case d.ch <- dlv:
	default:
		d.dropped.Add(1)
	}
}

func (d *DeliveryDispatcher) Stop() {
	d.mu.Lock()
	if !d.stopped {
		d.stopped = true
		close(d.ch)
	}
	d.mu.Unlock()
	d.wg.Wait()
}

func (d *DeliveryDispatcher) Dropped() int64 {
	return d.dropped.Load()
}

func (d *DeliveryDispatcher) Delivered() int64 {
	return d.delivered.Load()
}
