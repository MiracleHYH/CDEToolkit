package main

import (
	"fmt"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsruntime"
)

// Only the disposable CLI probe starts this process-exit watchdog. It is also
// effective if a monitor dies while a driver call is stuck and leaves an orphan.
// It never attempts native cleanup on a different OS thread. As with worker
// leases, a stopped process or kernel D state cannot provide hard realtime exit.
func startProbeWatchdog(clock gdsruntime.Clock, duration time.Duration, exit func(int)) (func(), error) {
	now, err := clock.Now()
	if err != nil {
		return nil, err
	}
	if now < 0 || duration <= 0 || now > int64(^uint64(0)>>1)-int64(duration) {
		return nil, fmt.Errorf("probe watchdog deadline overflow")
	}
	deadline := now + int64(duration)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now, err := clock.Now()
				if err != nil || now >= deadline {
					exit(124)
					return
				}
			}
		}
	}()
	return func() { close(done) }, nil
}
