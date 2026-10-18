//go:build linux && amd64 && cgo

package nvidia

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsruntime"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

func Available() bool { return true }

func (b *Backend) paths() (string, string) {
	n, c := b.nvmlPath, b.cudaPath
	if n == "" {
		n = "libnvidia-ml.so.1"
	}
	if c == "" {
		c = "libcuda.so.1"
	}
	return n, c
}

func (b *Backend) validate(uuid string) error {
	if !gdsapi.ValidUUID(uuid) {
		return fmt.Errorf("需要完整且规范的 GPU UUID")
	}
	if b.checkHost != nil {
		return b.checkHost()
	}
	return validateHost()
}

// Same-host PID visibility is a deployment prerequisite. These checks reject
// known containers and detectable partial views; they cannot prove that an
// unprivileged process is in the initial host namespace. The monitor must also
// verify the worker's exact GPU PID before acknowledging any holding state.
func validateHost() error {
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("GDS 首版不支持容器 PID 视图")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if os.Getenv("container") != "" {
		return fmt.Errorf("GDS 首版不支持容器")
	}
	for _, name := range []string{"pid", "time"} {
		self, selfErr := os.Readlink("/proc/self/ns/" + name)
		children, childrenErr := os.Readlink("/proc/self/ns/" + name + "_for_children")
		// Kernels before time namespaces have neither time entry and cannot
		// apply namespace clock offsets. Other missing information fails closed.
		if name == "time" && os.IsNotExist(selfErr) && os.IsNotExist(childrenErr) {
			continue
		}
		if selfErr != nil || childrenErr != nil || self != children {
			return fmt.Errorf("无法确认当前进程与子进程的 %s namespace 一致", name)
		}
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	seen := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[1] != strconv.Itoa(os.Getpid()) {
				return fmt.Errorf("不能确认宿主 PID 视图")
			}
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("内核未提供 PID namespace 身份")
	}
	if os.Getenv("CUDA_MPS_PIPE_DIRECTORY") != "" || os.Getenv("CUDA_MPS_LOG_DIRECTORY") != "" {
		return fmt.Errorf("GDS 首版不支持 MPS 环境")
	}
	return nil
}

func (b *Backend) Probe(uuid string) (sample gdsapi.Sample, err error) {
	sample.UUID = uuid
	if err = b.validate(uuid); err != nil {
		return sample, err
	}
	npath, cpath := b.paths()
	// CUDA's own UUID enumeration respects the caller's visibility, including
	// ordinal remapping. NVML alone would observe GPUs hidden by CUDA_VISIBLE_DEVICES.
	if err = visibleCUDA(cpath, uuid); err != nil {
		return sample, err
	}
	lib := nvml.New(nvml.WithLibraryPath(npath))
	if r := lib.Init(); r != nvml.SUCCESS {
		return sample, fmt.Errorf("NVML 初始化失败: %s", r.String())
	}
	defer func() {
		if r := lib.Shutdown(); r != nvml.SUCCESS && err == nil {
			err = fmt.Errorf("NVML 关闭失败: %s", r.String())
			sample.Complete = false
		}
	}()
	d, r := lib.DeviceGetHandleByUUID(uuid)
	if r != nvml.SUCCESS {
		return sample, fmt.Errorf("NVML UUID 查询失败: %s", r.String())
	}
	actual, r := d.GetUUID()
	if r != nvml.SUCCESS || actual != uuid {
		return sample, fmt.Errorf("NVML 实际 UUID 不匹配或未知: %s", r.String())
	}
	// NOT_SUPPORTED here is the documented answer for hardware without MIG
	// capability, not a missing activity or memory field interpreted as zero.
	current, pending, r := d.GetMigMode()
	if r != nvml.SUCCESS && r != nvml.ERROR_NOT_SUPPORTED {
		return sample, fmt.Errorf("MIG 模式不可确认: %s", r.String())
	}
	if r == nvml.SUCCESS && (current != nvml.DEVICE_MIG_DISABLE || pending != nvml.DEVICE_MIG_DISABLE) {
		return sample, fmt.Errorf("GDS 首版不支持 MIG 或待切换 MIG")
	}
	virtualization, r := d.GetVirtualizationMode()
	if r != nvml.SUCCESS || virtualization != nvml.GPU_VIRTUALIZATION_MODE_NONE {
		return sample, fmt.Errorf("vGPU/虚拟化模式不受支持或不可确认: %s", r.String())
	}
	mode, r := d.GetComputeMode()
	if r != nvml.SUCCESS || mode != nvml.COMPUTEMODE_DEFAULT {
		return sample, fmt.Errorf("仅支持 DEFAULT 计算模式: %s", r.String())
	}
	memory, r := d.GetMemoryInfo()
	if r != nvml.SUCCESS || memory.Total == 0 || memory.Total == ^uint64(0) || memory.Free == ^uint64(0) || memory.Used == ^uint64(0) || memory.Free > memory.Total || memory.Used > memory.Total || memory.Free > memory.Total-memory.Used {
		return sample, fmt.Errorf("NVML 显存信息不完整: %s", r.String())
	}
	utilization, r := d.GetUtilizationRates()
	if r != nvml.SUCCESS || utilization.Gpu > 100 || utilization.Memory > 100 {
		return sample, fmt.Errorf("NVML 利用率不可用: %s", r.String())
	}
	sample.ComputePIDs, err = queryProcesses(npath, uuid, 0)
	if err != nil {
		return sample, err
	}
	sample.GraphicsPIDs, err = queryProcesses(npath, uuid, 1)
	if err != nil {
		return sample, err
	}
	mps, err := queryProcesses(npath, uuid, 2)
	if err != nil {
		return sample, fmt.Errorf("MPS 归属不可确认: %w", err)
	}
	if len(mps) != 0 {
		return sample, fmt.Errorf("GDS 首版不支持 MPS 活动")
	}
	for _, pid := range append(append([]int(nil), sample.ComputePIDs...), sample.GraphicsPIDs...) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
			return sample, fmt.Errorf("GPU PID %d 无法在当前 PID 视图确认: %w", pid, err)
		}
	}
	sample.TotalBytes, sample.FreeBytes, sample.Utilization = memory.Total, memory.Free, int(utilization.Gpu)
	sample.Complete, sample.Supported = true, true
	return sample, nil
}

func visibleCUDA(path, uuid string) error {
	p, u := C.CString(path), C.CString(uuid)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(u))
	var problem [512]C.char
	if C.gds_cuda_visible(p, u, &problem[0], C.size_t(len(problem))) != 0 {
		return fmt.Errorf("CUDA 可见范围检查失败: %s", C.GoString(&problem[0]))
	}
	return nil
}

func queryProcesses(path, uuid string, kind int) ([]int, error) {
	p, u := C.CString(path), C.CString(uuid)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(u))
	var pids [4096]C.uint32_t
	count := C.uint(len(pids))
	var problem [512]C.char
	if C.gds_nvml_processes(p, u, C.int(kind), &pids[0], &count, &problem[0], C.size_t(len(problem))) != 0 {
		return nil, fmt.Errorf("NVML 进程列表不完整: %s", C.GoString(&problem[0]))
	}
	result := make([]int, int(count))
	for i := range result {
		if pids[i] == 0 {
			return nil, fmt.Errorf("NVML 返回无效 GPU PID")
		}
		result[i] = int(pids[i])
	}
	return result, nil
}

type allocation struct {
	ptr     *C.gds_cuda
	freeErr error
}

// Allocate and Free are called on the worker's one locked OS thread. The C
// binding checks the cleanup thread too, so accidental cross-thread cleanup
// cannot be acknowledged as successful release.
func (b *Backend) Allocate(uuid string, bytes, minFree uint64) (gdsruntime.Allocation, error) {
	if err := b.validate(uuid); err != nil {
		return nil, err
	}
	if bytes == 0 || minFree == 0 {
		return nil, fmt.Errorf("分配字节数与安全余量必须大于零")
	}
	// The monitor's preflight cannot authorize a later device state forever.
	// Recheck inside the disposable worker before creating its CUDA context:
	// MIG/vGPU/MPS or a process that appeared after the monitor's last sample
	// must not acquire an allocation merely because its UUID is still visible.
	sample, err := b.Probe(uuid)
	if err != nil {
		return nil, fmt.Errorf("worker 分配前设备检查失败: %w", err)
	}
	if len(sample.ComputePIDs) != 0 || len(sample.GraphicsPIDs) != 0 {
		return nil, fmt.Errorf("worker 分配前发现外部 GPU 进程")
	}
	_, path := b.paths()
	p, u := C.CString(path), C.CString(uuid)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(u))
	var problem [512]C.char
	a := C.gds_cuda_allocate(p, u, C.uint64_t(bytes), C.uint64_t(minFree), &problem[0], C.size_t(len(problem)))
	if a == nil {
		return nil, fmt.Errorf("CUDA 分配失败: %s", C.GoString(&problem[0]))
	}
	result := &allocation{ptr: a}
	// An idle MPS server can acquire its first client only when the context is
	// created. Require our own ordinary compute PID before the worker may emit
	// READY; an MPS server PID, MIG transition or concurrent process is not our
	// allocation ownership. The monitor independently checks kernel birth too.
	sample, err = b.Probe(uuid)
	if err == nil && (len(sample.ComputePIDs) != 1 || sample.ComputePIDs[0] != os.Getpid() || len(sample.GraphicsPIDs) != 0) {
		err = fmt.Errorf("worker CUDA context 的独占进程归属无法确认")
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("worker 分配后设备检查失败: %w", err), result.Free())
	}
	return result, nil
}

func (a *allocation) Free() error {
	if a.ptr == nil {
		return a.freeErr
	}
	var problem [512]C.char
	r := C.gds_cuda_free(a.ptr, &problem[0], C.size_t(len(problem)))
	if r != 0 {
		// Wrong-thread failure retains the object so the original worker thread
		// can still perform cleanup. Other failures require process exit.
		problemString := C.GoString(&problem[0])
		err := fmt.Errorf("CUDA 释放失败: %s", problemString)
		if r != C.GDS_CUDA_WRONG_THREAD {
			a.ptr = nil
			a.freeErr = err
		}
		return err
	}
	a.ptr = nil
	return nil
}
