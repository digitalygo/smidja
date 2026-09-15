package tui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var resizeSignal = syscall.SIGWINCH

var resizeRegistry struct {
	mu       sync.Mutex
	channels map[chan os.Signal]struct{}
}

func signalNotifyResize(channel chan os.Signal) {
	resizeRegistry.mu.Lock()
	if resizeRegistry.channels == nil {
		resizeRegistry.channels = make(map[chan os.Signal]struct{})
	}
	resizeRegistry.channels[channel] = struct{}{}
	resizeRegistry.mu.Unlock()
	signal.Notify(channel, resizeSignal)
}

func signalStopResize(channel chan os.Signal) {
	signal.Stop(channel)
	resizeRegistry.mu.Lock()
	delete(resizeRegistry.channels, channel)
	resizeRegistry.mu.Unlock()
}

func signalSelf(sig os.Signal) {
	_ = syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
}
