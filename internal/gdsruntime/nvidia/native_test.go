//go:build linux && amd64 && cgo

package nvidia

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const testUUID = "GPU-00112233-4455-6677-8899-aabbccddeeff"

func fakeLibrary(t *testing.T, extra ...string) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Fatal("native fake-ABI tests need a C compiler:", err)
	}
	path := filepath.Join(t.TempDir(), "libfake-nvidia.so")
	args := append([]string{"-shared", "-fPIC", "-O2", "-Wall", "-o", path, "testdata/fake_driver.c"}, extra...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile fake driver: %s: %v", out, err)
	}
	return path
}

func fakeBackend(path string) *Backend {
	return &Backend{nvmlPath: path, cudaPath: path, checkHost: func() error { return nil }}
}

func TestProbeFakeDriver(t *testing.T) {
	path := fakeLibrary(t)
	b := fakeBackend(path)
	t.Setenv("CUDA_VISIBLE_DEVICES", "1,0")
	s, err := b.Probe(testUUID)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Complete || !s.Supported || s.UUID != testUUID || s.TotalBytes != 16<<30 || s.FreeBytes != 12<<30 {
		t.Fatalf("incomplete sample: %+v", s)
	}
	for _, test := range []struct{ name, key, value string }{
		{"hidden", "CUDA_VISIBLE_DEVICES", "1"}, {"empty visibility", "CUDA_VISIBLE_DEVICES", ""},
		{"MIG", "GDS_FAKE_MIG", "1"}, {"pending MIG", "GDS_FAKE_MIG_PENDING", "1"},
		{"MIG unknown", "GDS_FAKE_MIG_RET", "999"}, {"virtualized", "GDS_FAKE_VIRT", "1"},
		{"virtualization unknown", "GDS_FAKE_VIRT_RET", "3"}, {"exclusive", "GDS_FAKE_MODE", "3"},
		{"mode unknown", "GDS_FAKE_MODE_RET", "3"}, {"memory unavailable", "GDS_FAKE_MEMORY_RET", "3"},
		{"util unavailable", "GDS_FAKE_UTIL_RET", "3"}, {"util invalid", "GDS_FAKE_UTIL", "101"},
		{"wrong UUID", "GDS_FAKE_BAD_UUID", "1"}, {"graphics unavailable", "GDS_FAKE_GRAPHICS_RET", "3"},
		{"compute unavailable", "GDS_FAKE_COMPUTE_RET", "4"}, {"MPS unknown", "GDS_FAKE_MPS_RET", "3"},
		{"MPS active", "GDS_FAKE_MPS_COUNT", "1"}, {"shutdown failed", "GDS_FAKE_SHUTDOWN_RET", "999"},
		{"process churn", "GDS_FAKE_CHURN", "1"}, {"process count limit", "GDS_FAKE_COMPUTE_COUNT", "1000000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			s, err := b.Probe(testUUID)
			if err == nil || s.Complete {
				t.Fatalf("unsafe successful sample %+v err=%v", s, err)
			}
		})
	}
	t.Run("MIG incapable hardware", func(t *testing.T) {
		t.Setenv("GDS_FAKE_MIG_RET", "3")
		s, err := b.Probe(testUUID)
		if err != nil || !s.Complete {
			t.Fatalf("documented non-MIG GPU rejected: %+v %v", s, err)
		}
	})
	t.Run("bounded list grows", func(t *testing.T) {
		t.Setenv("GDS_FAKE_COMPUTE_COUNT", "50")
		s, err := b.Probe(testUUID)
		if err != nil || len(s.ComputePIDs) != 50 {
			t.Fatalf("expanded process list: %+v %v", s, err)
		}
		for _, pid := range s.ComputePIDs {
			if pid != os.Getpid() {
				t.Fatalf("wrong process ABI: %d", pid)
			}
		}
	})
	for _, test := range []struct{ name, key, value string }{
		{"zero PID", "GDS_FAKE_PROCESS_PID", "0"},
		{"invisible PID", "GDS_FAKE_PROCESS_PID", "4294967295"},
		{"unexpected GPU instance", "GDS_FAKE_PROCESS_GPU_INSTANCE", "0"},
		{"unexpected compute instance", "GDS_FAKE_PROCESS_COMPUTE_INSTANCE", "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GDS_FAKE_COMPUTE_COUNT", "1")
			t.Setenv(test.key, test.value)
			if s, err := b.Probe(testUUID); err == nil || s.Complete {
				t.Fatalf("invalid process ownership accepted: %+v %v", s, err)
			}
		})
	}
}

func TestNVMLProcessVersionFallbacks(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			b := fakeBackend(fakeLibrary(t, "-DPROCESS_VERSION="+strconv.Itoa(version)))
			t.Setenv("GDS_FAKE_COMPUTE_COUNT", "9")
			t.Setenv("GDS_FAKE_GRAPHICS_COUNT", "2")
			s, err := b.Probe(testUUID)
			if err != nil || !s.Complete || len(s.ComputePIDs) != 9 || len(s.GraphicsPIDs) != 2 {
				t.Fatalf("process ABI v%d: %+v %v", version, s, err)
			}
			for _, pid := range append(s.ComputePIDs, s.GraphicsPIDs...) {
				if pid != os.Getpid() {
					t.Fatalf("process ABI v%d returned PID %d", version, pid)
				}
			}
		})
	}
}

// runCUDA binds each t.Run goroutine independently. A parent test's OS-thread
// lock does not extend to subtests; without this wrapper a postcheck rollback
// can migrate before Free, correctly fail its thread check, and leave the fake
// context alive for subsequent cases. The production worker already owns its
// locked thread throughout Allocate and Free.
func runCUDA(t *testing.T, name string, test func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		test(t)
	})
}

func TestCUDAAllocationFakeDriver(t *testing.T) {
	path := fakeLibrary(t)
	b := fakeBackend(path)
	t.Setenv("CUDA_VISIBLE_DEVICES", testUUID)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("GDS_FAKE_LOG", log)
	a, err := b.Allocate(testUUID, 1<<30, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Free(); err != nil {
		t.Fatal(err)
	}
	if err = a.Free(); err != nil {
		t.Fatal("idempotent free:", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"ctx-create", "mem-allocate", "mem-free", "ctx-destroy"} {
		if !strings.Contains(string(calls), call) {
			t.Fatalf("missing %s in %s", call, calls)
		}
	}
	for _, test := range []struct{ name, key, value string }{
		{"UUID outside visibility", "CUDA_VISIBLE_DEVICES", "1"},
		{"exclusive compute", "GDS_FAKE_CUDA_MODE", "3"},
		{"insufficient safety margin", "GDS_FAKE_FREE_BYTES", "1073741824"},
		{"context failed", "GDS_FAKE_CONTEXT_RET", "2"},
		{"memory query failed", "GDS_FAKE_INFO_RET", "999"},
		{"allocation race OOM", "GDS_FAKE_ALLOC_RET", "2"},
		{"post allocation safety margin", "GDS_FAKE_AFTER_FREE_BYTES", "0"},
		{"worker sees MIG mode", "GDS_FAKE_MIG", "1"},
		{"worker sees pending MIG", "GDS_FAKE_MIG_PENDING", "1"},
		{"worker sees virtualization", "GDS_FAKE_VIRT", "1"},
		{"worker sees MPS", "GDS_FAKE_MPS_COUNT", "1"},
		{"worker sees external compute", "GDS_FAKE_COMPUTE_COUNT", "1"},
		{"worker sees external graphics", "GDS_FAKE_GRAPHICS_COUNT", "1"},
	} {
		runCUDA(t, test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if a, err := b.Allocate(testUUID, 1<<30, 1<<30); err == nil {
				_ = a.Free()
				t.Fatal("unsafe allocation accepted")
			}
		})
	}
	runCUDA(t, "missing symbol", func(t *testing.T) {
		bad := fakeBackend(fakeLibrary(t, "-DOMIT_CUDA_ALLOC"))
		if _, err := bad.Allocate(testUUID, 1<<30, 1<<30); err == nil {
			t.Fatal("incomplete CUDA ABI accepted")
		}
	})
	for _, test := range []struct {
		name, setting string
		messages      []string
	}{
		{"allocation rollback free failure", "GDS_FAKE_FREE_RET", []string{"cuMemFree cleanup failed (999)"}},
		{"allocation rollback context failure", "GDS_FAKE_DESTROY_RET", []string{"cuCtxDestroy cleanup failed (999)"}},
		{"allocation rollback both failures", "both", []string{"cuMemFree cleanup failed (999)", "cuCtxDestroy cleanup failed (999)"}},
	} {
		runCUDA(t, test.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "allocation-rollback.log")
			t.Setenv("GDS_FAKE_LOG", log)
			t.Setenv("GDS_FAKE_AFTER_FREE_BYTES", "0")
			if test.setting == "both" {
				t.Setenv("GDS_FAKE_FREE_RET", "999")
				t.Setenv("GDS_FAKE_DESTROY_RET", "999")
			} else {
				t.Setenv(test.setting, "999")
			}
			_, err := b.Allocate(testUUID, 1<<30, 1<<30)
			if err == nil || !strings.Contains(err.Error(), "CUDA memory safety margin lost after allocation") {
				t.Fatalf("allocation rollback lost primary failure: %v", err)
			}
			for _, message := range test.messages {
				if !strings.Contains(err.Error(), message) {
					t.Fatalf("allocation rollback lost %s: %v", message, err)
				}
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range []string{"ctx-create", "mem-allocate", "mem-free", "ctx-destroy"} {
				if strings.Count(string(calls), call+"\n") != 1 {
					t.Fatalf("allocation rollback must perform one %s: %s", call, calls)
				}
			}
		})
	}
	for _, test := range []struct{ name, key, value string }{
		{"missing worker context", "GDS_FAKE_AFTER_COMPUTE_COUNT", "0"},
		{"ambiguous worker context", "GDS_FAKE_AFTER_COMPUTE_COUNT", "2"},
		{"other compute PID", "GDS_FAKE_AFTER_PROCESS_PID", strconv.Itoa(os.Getppid())},
		{"concurrent graphics", "GDS_FAKE_AFTER_GRAPHICS_COUNT", "1"},
		{"MPS acquired context", "GDS_FAKE_AFTER_MPS_COUNT", "1"},
	} {
		runCUDA(t, test.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "postcheck.log")
			t.Setenv("GDS_FAKE_LOG", log)
			t.Setenv(test.key, test.value)
			if a, err := b.Allocate(testUUID, 1<<30, 1<<30); err == nil {
				_ = a.Free()
				t.Fatal("unconfirmed worker context accepted")
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range []string{"ctx-create", "mem-allocate", "mem-free", "ctx-destroy"} {
				if strings.Count(string(calls), call+"\n") != 1 {
					t.Fatalf("postcheck failure must perform one %s: %s", call, calls)
				}
			}
		})
	}
	runCUDA(t, "postcheck cleanup failure is preserved", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "failed-cleanup.log")
		t.Setenv("GDS_FAKE_LOG", log)
		t.Setenv("GDS_FAKE_AFTER_MPS_COUNT", "1")
		t.Setenv("GDS_FAKE_FREE_RET", "999")
		if _, err := b.Allocate(testUUID, 1<<30, 1<<30); err == nil || !strings.Contains(err.Error(), "CUDA 释放失败") {
			t.Fatalf("postcheck lost cleanup failure: %v", err)
		}
		calls, err := os.ReadFile(log)
		if err != nil || strings.Count(string(calls), "mem-free\n") != 1 || strings.Count(string(calls), "ctx-destroy\n") != 1 {
			t.Fatalf("postcheck cleanup must attempt free and destroy: %s %v", calls, err)
		}
	})
	runCUDA(t, "free error preserved", func(t *testing.T) {
		a, err := b.Allocate(testUUID, 1<<30, 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("GDS_FAKE_FREE_RET", "999")
		if err = a.Free(); err == nil {
			t.Fatal("failed driver cleanup acknowledged")
		}
		if err = a.Free(); err == nil {
			t.Fatal("retry incorrectly acknowledged failed driver cleanup")
		}
	})
	runCUDA(t, "wrong cleanup thread is rejected", func(t *testing.T) {
		a, err := b.Allocate(testUUID, 1<<30, 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the owner thread responsible for cleanup even if an assertion
		// fails after the deliberately rejected cross-thread operation.
		defer func() {
			if err := a.Free(); err != nil {
				t.Errorf("owner-thread deferred cleanup: %v", err)
			}
		}()
		wrongThread := make(chan error, 1)
		go func() { runtime.LockOSThread(); defer runtime.UnlockOSThread(); wrongThread <- a.Free() }()
		if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "original OS thread") {
			t.Fatalf("wrong-thread cleanup: %v", err)
		}
		// A rejected Free must leave ownership and the allocation intact.
		// This deterministically models the leak behind the CI contamination,
		// without depending on the scheduler to migrate a test by chance.
		sample, err := b.Probe(testUUID)
		if err != nil || len(sample.ComputePIDs) != 1 || sample.ComputePIDs[0] != os.Getpid() {
			t.Fatalf("wrong-thread Free lost the live context: %+v %v", sample, err)
		}
		if err := a.Free(); err != nil {
			t.Fatal("original-thread cleanup:", err)
		}
		sample, err = b.Probe(testUUID)
		if err != nil || len(sample.ComputePIDs) != 0 {
			t.Fatalf("owner-thread cleanup left a context for the next case: %+v %v", sample, err)
		}
		next, err := b.Allocate(testUUID, 1<<30, 1<<30)
		if err != nil {
			t.Fatalf("allocation after owner cleanup: %v", err)
		}
		if err := next.Free(); err != nil {
			t.Fatal("subsequent allocation cleanup:", err)
		}
	})
}

func TestInvalidUUIDDoesNotOpenDriver(t *testing.T) {
	b := fakeBackend("/definitely-missing-driver")
	if _, err := b.Probe("0"); err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("invalid UUID: %v", err)
	}
	if _, err := b.Allocate("0", 1, 1); err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("invalid allocation UUID: %v", err)
	}
}
