// Package gdsruntime implements the driver-independent monitor and worker lease
// protocol. Native driver calls only occur in disposable probe/worker processes.
package gdsruntime

import (
	"fmt"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

const (
	ProbeTimeout    = 2 * time.Second
	LeaseDuration   = 15 * time.Second
	ReplyTimeout    = 2 * time.Second
	CleanupTimeout  = 2 * time.Second
	MaxMessageBytes = 16 << 10
)

type Action string

const (
	NoAction Action = ""
	Allocate Action = "allocate"
	Renew    Action = "renew"
	Release  Action = "release"
)

type Decision struct {
	Action         Action
	Phase, Problem string
	Bytes          uint64
	IdleSeconds    int64
}

// Machine uses the start time of each completed observation. No previous
// observation, failed query, restart, or unobserved interval contributes idle time.
type Machine struct {
	Config                     gdsapi.Config
	phase                      string
	idleSince, lastObservation int64
	haveIdle, haveObservation  bool
	backoffUntil               int64
	failures                   int
}

func NewMachine(c gdsapi.Config) (*Machine, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &Machine{Config: c, phase: "observing"}, nil
}
func (m *Machine) Phase() string { return m.phase }
func (m *Machine) resetIdle()    { m.haveIdle = false; m.idleSince = 0 }
func targetBytes(total uint64, percent int) uint64 {
	return total/100*uint64(percent) + (total%100)*uint64(percent)/100
}
func (m *Machine) Observe(s gdsapi.Sample, queryErr error, started, completed int64, workerPID int) Decision {
	d := Decision{}
	maxGap := int64(time.Duration(m.Config.ProbeIntervalSeconds)*time.Second + ProbeTimeout + time.Second)
	gap := m.haveObservation && (started < m.lastObservation || started-m.lastObservation > maxGap)
	if gap {
		m.resetIdle()
	}
	m.lastObservation = started
	m.haveObservation = true
	fail := func(phase, problem string) Decision {
		m.resetIdle()
		m.phase = phase
		d.Phase = phase
		d.Problem = problem
		if workerPID > 0 {
			d.Action = Release
			m.phase = "releasing"
			d.Phase = m.phase
		}
		return d
	}
	if gap && workerPID > 0 {
		return fail("unavailable", "GPU observation gap exceeded")
	}
	if queryErr != nil {
		return fail("unavailable", queryErr.Error())
	}
	if started < 0 || completed < started || completed-started > int64(ProbeTimeout) {
		return fail("unavailable", "probe observation expired")
	}
	if !s.Complete || !s.Supported || s.UUID != m.Config.GPUUUID || s.TotalBytes == 0 || s.FreeBytes > s.TotalBytes || s.Utilization < 0 || s.Utilization > 100 {
		problem := s.Problem
		if problem == "" {
			problem = "incomplete or unsupported GPU observation"
		}
		return fail("unavailable", problem)
	}
	bytes := targetBytes(s.TotalBytes, m.Config.MemoryPercent)
	reserve := uint64(m.Config.MinFreeMiB) << 20
	if bytes == 0 || reserve > s.TotalBytes || bytes > s.TotalBytes-reserve {
		return fail("unavailable", "configured capacity exceeds device memory")
	}
	busy := s.Utilization > m.Config.MaxIdleUtilizationPercent
	workerVisible := false
	for _, pids := range [][]int{s.ComputePIDs, s.GraphicsPIDs} {
		for _, pid := range pids {
			if workerPID > 0 && pid == workerPID {
				workerVisible = true
			}
			if pid <= 0 || workerPID <= 0 || pid != workerPID {
				busy = true
			}
		}
	}
	if workerPID > 0 && !workerVisible {
		return fail("unavailable", "worker GPU context ownership not visible")
	}
	if busy {
		return fail("observing", "external GPU activity")
	}
	if s.FreeBytes < reserve || (workerPID == 0 && (bytes > s.FreeBytes || reserve > s.FreeBytes-bytes)) {
		return fail("observing", "insufficient free GPU memory")
	}
	if workerPID > 0 {
		m.resetIdle()
		m.phase = "holding"
		return Decision{Action: Renew, Phase: m.phase, Bytes: bytes}
	}
	if started < m.backoffUntil {
		m.resetIdle()
		m.phase = "backoff"
		return Decision{Phase: m.phase, Problem: "worker retry backoff"}
	}
	if !m.haveIdle {
		m.idleSince = started
		m.haveIdle = true
	}
	idle := (started - m.idleSince) / int64(time.Second)
	m.phase = "idle"
	d = Decision{Phase: m.phase, IdleSeconds: idle, Bytes: bytes}
	if idle >= int64(m.Config.IdleSeconds) {
		m.phase = "starting-worker"
		d.Phase = m.phase
		d.Action = Allocate
		m.resetIdle()
	}
	return d
}
func (m *Machine) WorkerFailed(now int64) {
	m.resetIdle()
	m.failures++
	shift := m.failures - 1
	if shift > 4 {
		shift = 4
	}
	delay := 5 * time.Second * time.Duration(1<<shift)
	if delay > time.Minute {
		delay = time.Minute
	}
	m.backoffUntil = now + int64(delay)
	m.phase = "backoff"
}
func (m *Machine) WorkerReleased()  { m.resetIdle(); m.phase = "observing" }
func (m *Machine) WorkerConfirmed() { m.failures = 0; m.phase = "holding" }
func (m *Machine) FreshPreflight(s gdsapi.Sample, err error, started, completed int64) error {
	// Use an independent machine so a failed preflight never inherits idle credit.
	fresh, _ := NewMachine(m.Config)
	d := fresh.Observe(s, err, started, completed, 0)
	if d.Phase != "idle" {
		return fmt.Errorf("worker preflight: %s", d.Problem)
	}
	m.lastObservation = started
	m.haveObservation = true
	return nil
}
