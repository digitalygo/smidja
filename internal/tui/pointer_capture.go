package tui

import (
	"sync"
	"time"
)

const selectionAutoscrollInterval = 40 * time.Millisecond

type transcriptPointerCapture struct {
	box         *LayoutBox
	generation  uint64
	layoutEpoch uint64
	anchor      SelectionPoint
	linkTarget  string
	hasLink     bool
	pressX      int
	pressY      int
	pointerX    int
	moved       bool
}

func newTranscriptPointerCapture(box *LayoutBox, generation, layoutEpoch uint64, anchor SelectionPoint, x, y int) *transcriptPointerCapture {
	return &transcriptPointerCapture{
		box:         box,
		generation:  generation,
		layoutEpoch: layoutEpoch,
		anchor:      anchor,
		pressX:      x,
		pressY:      y,
	}
}

type autoscrollController struct {
	mu     sync.Mutex
	dir    int
	active bool
	stopCh chan struct{}
	doneCh chan struct{}
}

func (c *autoscrollController) running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func (c *autoscrollController) direction() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dir
}

func (c *autoscrollController) start(dir int, interval time.Duration, step func(int)) {
	if dir == 0 {
		c.stop()
		return
	}
	c.mu.Lock()
	if c.active {
		if c.dir == dir {
			c.mu.Unlock()
			return
		}
		stopCh, doneCh := c.stopCh, c.doneCh
		c.active = false
		c.mu.Unlock()
		close(stopCh)
		<-doneCh
		c.start(dir, interval, step)
		return
	}
	if interval <= 0 {
		interval = selectionAutoscrollInterval
	}
	c.dir = dir
	c.active = true
	c.stopCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	stopCh, doneCh := c.stopCh, c.doneCh
	c.mu.Unlock()
	go c.loop(dir, interval, step, stopCh, doneCh)
}

func (c *autoscrollController) loop(dir int, interval time.Duration, step func(int), stopCh, doneCh chan struct{}) {
	defer close(doneCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if step != nil {
				step(dir)
			}
		}
	}
}

func (c *autoscrollController) stop() {
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return
	}
	c.active = false
	stopCh, doneCh := c.stopCh, c.doneCh
	c.stopCh, c.doneCh = nil, nil
	c.mu.Unlock()
	close(stopCh)
	<-doneCh
}
