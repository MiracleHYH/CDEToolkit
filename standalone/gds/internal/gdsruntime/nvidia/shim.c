//go:build linux && amd64 && cgo

// Small runtime-loaded CUDA Driver API binding. No CUDA toolkit headers,
// libcudart, primary/shared contexts, asynchronous operations or kernels.
#include "shim.h"
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stddef.h>
#include <unistd.h>
#include <sys/syscall.h>

typedef int CUresult;
typedef int CUdevice;
typedef void *CUcontext;
typedef unsigned long long CUdeviceptr;
typedef struct { unsigned char bytes[16]; } CUuuid;

struct gds_cuda {
    void *library;
    CUcontext context;
    CUdeviceptr memory;
    long owner;
    CUresult (*init)(unsigned int);
    CUresult (*count)(int *);
    CUresult (*device)(CUdevice *, int);
    CUresult (*uuid)(CUuuid *, CUdevice);
    CUresult (*attribute)(int *, int, CUdevice);
    CUresult (*create)(CUcontext *, unsigned int, CUdevice);
    CUresult (*destroy)(CUcontext);
    CUresult (*info)(size_t *, size_t *);
    CUresult (*allocate)(CUdeviceptr *, size_t);
    CUresult (*release)(CUdeviceptr);
};

static int failure(char *err, size_t n, const char *what, int code) {
    snprintf(err, n, "%s (%d)", what, code);
    return -1;
}

static void append_cleanup_failure(char *err, size_t n, const char *what, int code) {
    size_t used = strnlen(err, n);
    if (used < n) snprintf(err + used, n - used, "; %s (%d)", what, code);
}

static gds_cuda *cuda_open(const char *path, char *err, size_t n) {
    gds_cuda *c = calloc(1, sizeof(*c));
    if (!c) { failure(err, n, "CUDA binding allocation failed", -1); return NULL; }
    c->library = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!c->library) {
        snprintf(err, n, "cannot load CUDA driver: %s", dlerror());
        free(c); return NULL;
    }
#define LOAD(field, symbol) do { *(void **)(&c->field) = dlsym(c->library, symbol); \
    if (!c->field) { snprintf(err, n, "missing CUDA symbol %s", symbol); goto fail; } } while(0)
    LOAD(init, "cuInit");
    LOAD(count, "cuDeviceGetCount");
    LOAD(device, "cuDeviceGet");
    *(void **)(&c->uuid) = dlsym(c->library, "cuDeviceGetUuid_v2");
    if (!c->uuid) LOAD(uuid, "cuDeviceGetUuid");
    LOAD(attribute, "cuDeviceGetAttribute");
    LOAD(create, "cuCtxCreate_v2");
    LOAD(destroy, "cuCtxDestroy_v2");
    LOAD(info, "cuMemGetInfo_v2");
    LOAD(allocate, "cuMemAlloc_v2");
    LOAD(release, "cuMemFree_v2");
#undef LOAD
    int r = c->init(0);
    if (r) { failure(err, n, "cuInit failed", r); goto fail; }
    return c;
fail:
    dlclose(c->library); free(c); return NULL;
}

static void format_uuid(const CUuuid *u, char *out) {
    const unsigned char *b = u->bytes;
    snprintf(out, 41, "GPU-%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
        b[0],b[1],b[2],b[3],b[4],b[5],b[6],b[7],b[8],b[9],b[10],b[11],b[12],b[13],b[14],b[15]);
}

static int find_device(gds_cuda *c, const char *wanted, CUdevice *device, char *err, size_t n) {
    int count = 0, r = c->count(&count);
    if (r) return failure(err, n, "cuDeviceGetCount failed", r);
    if (count < 0 || count > 4096) return failure(err, n, "invalid CUDA visible device count", count);
    for (int i = 0; i < count; ++i) {
        CUdevice d; CUuuid u; char actual[41];
        r = c->device(&d, i);
        if (r) return failure(err, n, "cuDeviceGet failed", r);
        r = c->uuid(&u, d);
        if (r) return failure(err, n, "cuDeviceGetUuid failed", r);
        format_uuid(&u, actual);
        if (!strcmp(wanted, actual)) {
            int mode = -1;
            // CU_DEVICE_ATTRIBUTE_COMPUTE_MODE = 20; DEFAULT = 0.
            r = c->attribute(&mode, 20, d);
            if (r) return failure(err, n, "CUDA compute mode is unknown", r);
            if (mode != 0) return failure(err, n, "CUDA compute mode is not DEFAULT", mode);
            *device = d; return 0;
        }
    }
    return failure(err, n, "selected UUID is outside CUDA visible devices", -1);
}

int gds_cuda_visible(const char *path, const char *uuid, char *err, size_t n) {
    gds_cuda *c = cuda_open(path, err, n);
    if (!c) return -1;
    CUdevice d;
    int r = find_device(c, uuid, &d, err, n);
    dlclose(c->library); free(c); return r;
}

gds_cuda *gds_cuda_allocate(const char *path, const char *uuid, uint64_t bytes,
                           uint64_t min_free, char *err, size_t n) {
    gds_cuda *c = cuda_open(path, err, n);
    if (!c) return NULL;
    CUdevice d; size_t available = 0, total = 0;
    if (find_device(c, uuid, &d, err, n)) goto fail;
    c->owner = syscall(SYS_gettid);
    int r = c->create(&c->context, 0, d);
    if (r) { c->context = NULL; failure(err, n, "cuCtxCreate failed", r); goto fail; }
    // Verify again after context creation: its own overhead and competing
    // allocations must fit in the safety margin before reserving any bytes.
    r = c->info(&available, &total);
    if (r) { failure(err, n, "cuMemGetInfo failed", r); goto fail; }
    if (!bytes || available > total || min_free > available || bytes > available - min_free || bytes > total) {
        failure(err, n, "insufficient free CUDA memory with safety margin", -1); goto fail;
    }
    r = c->allocate(&c->memory, (size_t)bytes);
    if (r) { c->memory = 0; failure(err, n, "cuMemAlloc failed", r); goto fail; }
    r = c->info(&available, &total);
    if (r || available > total || available < min_free) {
        failure(err, n, "CUDA memory safety margin lost after allocation", r); goto fail;
    }
    return c;
fail: {
    int released = 0, destroyed = 0;
    if (c->memory) released = c->release(c->memory);
    if (c->context) destroyed = c->destroy(c->context);
    // Preserve the allocation failure as the primary cause while reporting
    // both cleanup failures. The disposable worker must still exit if either
    // native cleanup operation could not confirm release.
    if (released) append_cleanup_failure(err, n, "cuMemFree cleanup failed", released);
    if (destroyed) append_cleanup_failure(err, n, "cuCtxDestroy cleanup failed", destroyed);
    dlclose(c->library); free(c); return NULL;
}
}

int gds_cuda_free(gds_cuda *c, char *err, size_t n) {
    if (!c) return 0;
    if (c->owner != syscall(SYS_gettid)) {
        failure(err, n, "CUDA cleanup must run on original OS thread", -1);
        return GDS_CUDA_WRONG_THREAD;
    }
    int released = 0, destroyed = 0;
    if (c->memory) released = c->release(c->memory);
    if (c->context) destroyed = c->destroy(c->context);
    // A failed cleanup is reported. The worker must exit; it cannot report
    // release success merely because it sent a cleanup request to the driver.
    dlclose(c->library); free(c);
    if (released || destroyed) return failure(err, n, "CUDA context cleanup failed", released ? released : destroyed);
    return 0;
}

// go-nvml's high-level Get*RunningProcesses helpers retry indefinitely on
// INSUFFICIENT_SIZE. This bounded adapter uses the documented versioned NVML
// ABI instead, refuses truncation, and never allocates more than 4096 entries.
// Scalar properties and capability queries continue to use official go-nvml.
typedef struct { unsigned int pid; unsigned long long used; } process_v1;
typedef struct { unsigned int pid; unsigned long long used; unsigned int gpu, compute; } process_v2;
_Static_assert(sizeof(process_v1) == 16 && offsetof(process_v1, used) == 8,
               "unexpected Linux amd64 nvmlProcessInfo_v1_t ABI");
_Static_assert(sizeof(process_v2) == 24 && offsetof(process_v2, gpu) == 16,
               "unexpected Linux amd64 nvmlProcessInfo_v2_t ABI");

int gds_nvml_processes(const char *path, const char *uuid, int kind,
                       uint32_t *pids, unsigned int *out_count, char *err, size_t n) {
    void *lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!lib) { snprintf(err, n, "cannot load NVML for process query: %s", dlerror()); return -1; }
    int (*handle)(const char *, void **);
    *(void **)(&handle) = dlsym(lib, "nvmlDeviceGetHandleByUUID");
    void *device = NULL;
    if (!handle) { failure(err,n,"missing NVML UUID symbol",-1); goto fail; }
    int r = handle(uuid, &device);
    if (r) { failure(err,n,"NVML UUID lookup failed",r); goto fail; }
    const char *base = kind == 0 ? "nvmlDeviceGetComputeRunningProcesses" :
                       kind == 1 ? "nvmlDeviceGetGraphicsRunningProcesses" : "nvmlDeviceGetMPSComputeRunningProcesses";
    int (*query)(void *, unsigned int *, void *) = NULL;
    size_t stride = 0; char symbol[96];
    for (int version = 3; version >= 1; --version) {
        if (version == 1) snprintf(symbol, sizeof(symbol), "%s", base);
        else snprintf(symbol, sizeof(symbol), "%s_v%d", base, version);
        *(void **)(&query) = dlsym(lib, symbol);
        // The v3 function still takes nvmlProcessInfo_t (the v2 layout).
        if (query) { stride = version >= 2 ? sizeof(process_v2) : sizeof(process_v1); break; }
    }
    if (!query) { failure(err,n,"missing complete NVML process query",-1); goto fail; }
    unsigned int capacity = 8;
    for (int attempt = 0; attempt < 4; ++attempt) {
        void *entries = calloc(capacity, stride);
        if (!entries) { failure(err,n,"NVML process buffer allocation failed",-1); goto fail; }
        unsigned int count = capacity;
        r = query(device, &count, entries);
        if (!r && count <= capacity && count <= *out_count) {
            for (unsigned int i = 0; i < count; ++i) {
                if (stride == sizeof(process_v2)) {
                    process_v2 *entry = (process_v2 *)((char *)entries + i*stride);
                    // A physical GPU in non-MIG mode must have both instance
                    // IDs set to NVML_VALUE_NOT_AVAILABLE. Do not discard this
                    // ownership evidence if MIG changed after the mode query.
                    if (entry->gpu != UINT32_MAX || entry->compute != UINT32_MAX) {
                        free(entries);
                        failure(err,n,"NVML process belongs to a MIG instance",-1);
                        goto fail;
                    }
                }
                memcpy(&pids[i], (char *)entries + i*stride, sizeof(uint32_t));
            }
            *out_count = count; free(entries); dlclose(lib); return 0;
        }
        free(entries);
        if (r != 7) { failure(err,n,"NVML process query incomplete or failed",r); goto fail; } // INSUFFICIENT_SIZE
        if (count > 4096 || capacity >= 4096) break;
        capacity = count > capacity ? count + 1 : capacity * 2;
        if (capacity > 4096) capacity = 4096;
    }
    failure(err,n,"NVML process query did not stabilize within bounds",7);
fail:
    dlclose(lib); return -1;
}
