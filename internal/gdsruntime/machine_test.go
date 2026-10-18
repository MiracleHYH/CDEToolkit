package gdsruntime

import (
	"errors"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

const testUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"

func sample() gdsapi.Sample {
	return gdsapi.Sample{UUID: testUUID, TotalBytes: 8 << 30, FreeBytes: 7 << 30, Utilization: 0, Complete: true, Supported: true}
}
func config() gdsapi.Config {
	c := gdsapi.DefaultConfig(testUUID)
	c.IdleSeconds = 3
	c.ProbeIntervalSeconds = 1
	return c
}
func observation(m *Machine, s gdsapi.Sample, err error, seconds int64, pid int) Decision {
	start := seconds * int64(time.Second)
	return m.Observe(s, err, start, start+int64(time.Millisecond), pid)
}
func TestMachineContinuousIdleAndReset(t *testing.T) {
	m, _ := NewMachine(config())
	s := sample()
	for i := int64(0); i < 3; i++ {
		if d := observation(m, s, nil, i, 0); d.Action != NoAction || d.IdleSeconds != i {
			t.Fatalf("idle %d: %+v", i, d)
		}
	}
	if d := observation(m, s, nil, 3, 0); d.Action != Allocate {
		t.Fatalf("threshold: %+v", d)
	}
	// A new machine never restores persisted credit from an earlier run.
	m, _ = NewMachine(config())
	if d := observation(m, s, nil, 999, 0); d.Action != NoAction || d.IdleSeconds != 0 {
		t.Fatalf("restart: %+v", d)
	}
	for name, mutation := range map[string]func(*gdsapi.Sample){"compute": func(s *gdsapi.Sample) { s.ComputePIDs = []int{100} }, "graphics": func(s *gdsapi.Sample) { s.GraphicsPIDs = []int{100} }, "incomplete": func(s *gdsapi.Sample) { s.Complete = false }, "low-free": func(s *gdsapi.Sample) { s.FreeBytes = 1 << 30 }, "uuid": func(s *gdsapi.Sample) { s.UUID = "GPU-other" }, "utilization": func(s *gdsapi.Sample) { s.Utilization = 99 }} {
		t.Run(name, func(t *testing.T) {
			m, _ := NewMachine(config())
			observation(m, s, nil, 0, 0)
			observation(m, s, nil, 1, 0)
			changed := s
			mutation(&changed)
			d := observation(m, changed, nil, 2, 0)
			if d.Action == Allocate {
				t.Fatal("bad observation allocated")
			}
			d = observation(m, s, nil, 3, 0)
			if d.IdleSeconds != 0 || d.Action != NoAction {
				t.Fatalf("failed reset: %+v", d)
			}
		})
	}
}
func TestMachineObservationGapAndErrors(t *testing.T) {
	m, _ := NewMachine(config())
	s := sample()
	observation(m, s, nil, 0, 0)
	observation(m, s, nil, 1, 0)
	if d := observation(m, s, nil, 30, 0); d.IdleSeconds != 0 || d.Action != NoAction {
		t.Fatalf("unobserved gap counted: %+v", d)
	}
	observation(m, s, errors.New("timeout"), 31, 0)
	if d := observation(m, s, nil, 32, 0); d.IdleSeconds != 0 {
		t.Fatal(d)
	}
	d := m.Observe(s, nil, int64(33*time.Second), int64(36*time.Second), 0)
	if d.Phase != "unavailable" {
		t.Fatal(d)
	}
	s.ComputePIDs = []int{200}
	observation(m, s, nil, 40, 200)
	if d := observation(m, s, nil, 60, 200); d.Action != Release {
		t.Fatalf("holding gap: %+v", d)
	}
}
func TestMachineWorkerOwnershipAndReserve(t *testing.T) {
	for name, s := range map[string]gdsapi.Sample{"missing-context": sample(), "external-compute": func() gdsapi.Sample { s := sample(); s.ComputePIDs = []int{200, 300}; return s }(), "external-graphics": func() gdsapi.Sample { s := sample(); s.ComputePIDs = []int{200}; s.GraphicsPIDs = []int{300}; return s }(), "low-free": func() gdsapi.Sample { s := sample(); s.ComputePIDs = []int{200}; s.FreeBytes = 10; return s }()} {
		t.Run(name, func(t *testing.T) {
			m, _ := NewMachine(config())
			if d := observation(m, s, nil, 0, 200); d.Action != Release {
				t.Fatalf("unsafe renewal: %+v", d)
			}
		})
	}
	m, _ := NewMachine(config())
	s := sample()
	s.ComputePIDs = []int{200}
	s.FreeBytes = 1 << 30
	if d := observation(m, s, nil, 0, 200); d.Action != Renew {
		t.Fatalf("holding uses reserve only: %+v", d)
	}
}
func TestMachineBackoffAndFreshPreflight(t *testing.T) {
	m, _ := NewMachine(config())
	s := sample()
	m.WorkerFailed(0)
	if d := observation(m, s, nil, 4, 0); d.Phase != "backoff" {
		t.Fatal(d)
	}
	if d := observation(m, s, nil, 5, 0); d.IdleSeconds != 0 || d.Action != NoAction {
		t.Fatal(d)
	}
	s.ComputePIDs = []int{77}
	if err := m.FreshPreflight(s, nil, 0, 1); err == nil {
		t.Fatal("busy preflight accepted")
	}
	if targetBytes(^uint64(0), 50) != ^uint64(0)/2 {
		t.Fatal("target overflow")
	}
}
func TestLeaseValidatorAbsoluteIdentitySequenceAndAllocation(t *testing.T) {
	fresh := func() leaseValidator { return leaseValidator{uuid: testUUID, token: "0123456789abcdef", boot: "boot"} }                                                          // gitleaks:allow deterministic CPU protocol fixture, not a credential
	lease := gdsapi.Lease{Type: "ALLOCATE", Protocol: 1, Boot: "boot", Token: "0123456789abcdef", UUID: testUUID, Seq: 1, ExpiresAtBootNS: 1000, Bytes: 100, MinFreeBytes: 10} // gitleaks:allow deterministic CPU protocol fixture, not a credential
	v := fresh()
	if err := v.accept(lease, 900); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*gdsapi.Lease){"replay": func(l *gdsapi.Lease) { l.Type = "RENEW" }, "wrong-boot": func(l *gdsapi.Lease) { l.Seq = 2; l.Type = "RENEW"; l.Boot = "other" }, "changed-bytes": func(l *gdsapi.Lease) { l.Seq = 2; l.Type = "RENEW"; l.Bytes++ }, "deadline": func(l *gdsapi.Lease) { l.Seq = 2; l.Type = "RENEW"; l.ExpiresAtBootNS = 900 }, "reallocate": func(l *gdsapi.Lease) { l.Seq = 2 }} {
		t.Run(name, func(t *testing.T) {
			l := lease
			change(&l)
			copy := v
			if err := copy.accept(l, 900); err == nil {
				t.Fatal("invalid lease accepted")
			}
		})
	}
	v = fresh()
	if err := v.accept(lease, 1000); err == nil {
		t.Fatal("buffered expired ALLOCATE revived")
	}
	v = fresh()
	if err := v.accept(lease, 900); err != nil {
		t.Fatal(err)
	}
	renewal := lease
	renewal.Type = "RENEW"
	renewal.Seq = 2
	renewal.ExpiresAtBootNS = 2000
	if err := v.accept(renewal, 1001); err == nil {
		t.Fatal("future renewal revived a previously expired lease")
	}
	v = fresh()
	lease.Seq = 2
	lease.Type = "RENEW"
	if err := v.accept(lease, 900); err == nil {
		t.Fatal("RENEW allocated without ALLOCATE")
	}
}
