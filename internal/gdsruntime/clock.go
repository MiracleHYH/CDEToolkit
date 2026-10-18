package gdsruntime

import "fmt"

// Clock returns CLOCK_BOOTTIME nanoseconds shared by processes in this boot and
// time namespace. It must include suspend time; wall clocks cannot issue leases.
type Clock interface{ Now() (int64, error) }

type BootClock struct{}

func (BootClock) Now() (int64, error) { return bootTimeNS() }

func checkedDeadline(now, duration int64) (int64, error) {
	if now < 0 || duration <= 0 || now > int64(^uint64(0)>>1)-duration {
		return 0, fmt.Errorf("invalid boottime deadline")
	}
	return now + duration, nil
}
