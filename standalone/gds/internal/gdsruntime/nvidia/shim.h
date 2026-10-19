#ifndef NIDO_GDS_SHIM_H
#define NIDO_GDS_SHIM_H
#include <stdint.h>
#include <stddef.h>

typedef struct gds_cuda gds_cuda;
// No cleanup was attempted; the original thread may still release the object.
#define GDS_CUDA_WRONG_THREAD (-2)
int gds_cuda_visible(const char *path, const char *uuid, char *err, size_t errlen);
gds_cuda *gds_cuda_allocate(const char *path, const char *uuid, uint64_t bytes,
                           uint64_t min_free, char *err, size_t errlen);
int gds_cuda_free(gds_cuda *allocation, char *err, size_t errlen);
int gds_nvml_processes(const char *path, const char *uuid, int kind,
                       uint32_t *pids, unsigned int *count, char *err, size_t errlen);
#endif
