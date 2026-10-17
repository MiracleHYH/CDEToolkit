#include <cuda_runtime.h>

#include <cerrno>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <string>
#include <unistd.h>

namespace {

volatile std::sig_atomic_t stop_requested = 0;

void handle_signal(int) {
  stop_requested = 1;
}

void usage(const char* program) {
  std::printf(
      "Usage: %s --device INDEX --memory-percent PERCENT --status-file PATH\n",
      program);
}

bool parse_unsigned(const char* text, unsigned int* value) {
  if (text == nullptr || *text == '\0' || *text == '-') {
    return false;
  }
  errno = 0;
  char* end = nullptr;
  const unsigned long parsed = std::strtoul(text, &end, 10);
  if (errno != 0 || end == text || *end != '\0' ||
      parsed > std::numeric_limits<unsigned int>::max()) {
    return false;
  }
  *value = static_cast<unsigned int>(parsed);
  return true;
}

bool cuda_ok(cudaError_t result, const char* operation) {
  if (result == cudaSuccess) {
    return true;
  }
  std::fprintf(stderr, "scheduler: %s failed: %s\n", operation,
               cudaGetErrorString(result));
  return false;
}

bool write_status(const std::string& path, unsigned int device,
                  unsigned int memory_percent, size_t allocated_bytes,
                  size_t total_bytes) {
  const std::string temporary =
      path + "." + std::to_string(static_cast<long long>(getpid())) + ".tmp";
  FILE* output = std::fopen(temporary.c_str(), "w");
  if (output == nullptr) {
    std::fprintf(stderr, "scheduler: cannot open status file %s: %s\n",
                 temporary.c_str(), std::strerror(errno));
    return false;
  }
  std::fprintf(output, "pid=%ld\n", static_cast<long>(getpid()));
  std::fprintf(output, "device=%u\n", device);
  std::fprintf(output, "memory_percent=%u\n", memory_percent);
  std::fprintf(output, "allocated_bytes=%zu\n", allocated_bytes);
  std::fprintf(output, "total_bytes=%zu\n", total_bytes);
  if (std::fclose(output) != 0 || std::rename(temporary.c_str(), path.c_str()) != 0) {
    std::fprintf(stderr, "scheduler: cannot publish status file %s: %s\n",
                 path.c_str(), std::strerror(errno));
    unlink(temporary.c_str());
    return false;
  }
  return true;
}

__global__ void scheduler_kernel(unsigned long long cycles,
                                 unsigned long long* sink) {
  const unsigned long long started = clock64();
  unsigned long long value = *sink ^ started;
  while (clock64() - started < cycles) {
    value = value * 2862933555777941757ULL + 3037000493ULL;
  }
  *sink = value;
}

}  // namespace

int main(int argc, char** argv) {
  unsigned int device = 0;
  unsigned int memory_percent = 25;
  std::string status_file;

  for (int index = 1; index < argc; ++index) {
    if (std::strcmp(argv[index], "--help") == 0 ||
        std::strcmp(argv[index], "-h") == 0) {
      usage(argv[0]);
      return 0;
    }
    if (index + 1 >= argc) {
      usage(argv[0]);
      return 2;
    }
    if (std::strcmp(argv[index], "--device") == 0) {
      if (!parse_unsigned(argv[++index], &device)) {
        std::fprintf(stderr, "scheduler: invalid device index\n");
        return 2;
      }
    } else if (std::strcmp(argv[index], "--memory-percent") == 0) {
      if (!parse_unsigned(argv[++index], &memory_percent) ||
          memory_percent == 0 || memory_percent > 90) {
        std::fprintf(stderr, "scheduler: memory percent must be between 1 and 90\n");
        return 2;
      }
    } else if (std::strcmp(argv[index], "--status-file") == 0) {
      status_file = argv[++index];
      if (status_file.empty()) {
        std::fprintf(stderr, "scheduler: status file cannot be empty\n");
        return 2;
      }
    } else {
      std::fprintf(stderr, "scheduler: unknown argument: %s\n", argv[index]);
      usage(argv[0]);
      return 2;
    }
  }

  if (status_file.empty()) {
    std::fprintf(stderr, "scheduler: --status-file is required\n");
    return 2;
  }

  std::signal(SIGINT, handle_signal);
  std::signal(SIGTERM, handle_signal);
  std::signal(SIGHUP, handle_signal);

  if (!cuda_ok(cudaSetDevice(device), "cudaSetDevice") ||
      !cuda_ok(cudaSetDeviceFlags(cudaDeviceScheduleBlockingSync),
               "cudaSetDeviceFlags")) {
    return 1;
  }

  cudaDeviceProp properties{};
  if (!cuda_ok(cudaGetDeviceProperties(&properties, device),
               "cudaGetDeviceProperties")) {
    return 1;
  }

  size_t free_bytes = 0;
  size_t total_bytes = 0;
  if (!cuda_ok(cudaMemGetInfo(&free_bytes, &total_bytes), "cudaMemGetInfo")) {
    return 1;
  }
  const size_t allocated_bytes =
      static_cast<size_t>((static_cast<unsigned long long>(total_bytes) *
                           memory_percent) /
                          100ULL);
  if (allocated_bytes < sizeof(unsigned long long) || allocated_bytes > free_bytes) {
    std::fprintf(stderr,
                 "scheduler: requested %zu bytes but only %zu bytes are free\n",
                 allocated_bytes, free_bytes);
    return 1;
  }

  void* allocation = nullptr;
  if (!cuda_ok(cudaMalloc(&allocation, allocated_bytes), "cudaMalloc")) {
    return 1;
  }
  if (!cuda_ok(cudaMemset(allocation, 0, sizeof(unsigned long long)),
               "cudaMemset")) {
    cudaFree(allocation);
    return 1;
  }
  if (!write_status(status_file, device, memory_percent, allocated_bytes,
                    total_bytes)) {
    cudaFree(allocation);
    return 1;
  }

  std::printf(
      "scheduler ready: pid=%ld device=%u name=%s memory=%zu/%zu bytes (%u%%)\n",
      static_cast<long>(getpid()), device, properties.name, allocated_bytes,
      total_bytes, memory_percent);
  std::fflush(stdout);

  // A bounded 300 ms kernel is relaunched continuously so utilization probes do
  // not land in an idle gap. One GPU thread is sufficient, so actual compute
  // contention stays minimal; blocking driver synchronization keeps host CPU low.
  const unsigned long long kernel_cycles =
      static_cast<unsigned long long>(properties.clockRate) * 300ULL;
  while (!stop_requested) {
    scheduler_kernel<<<1, 1>>>(
        kernel_cycles, static_cast<unsigned long long*>(allocation));
    if (!cuda_ok(cudaGetLastError(), "kernel launch") ||
        !cuda_ok(cudaDeviceSynchronize(), "cudaDeviceSynchronize")) {
      unlink(status_file.c_str());
      cudaFree(allocation);
      return 1;
    }
  }

  unlink(status_file.c_str());
  cudaFree(allocation);
  cudaDeviceReset();
  return 0;
}
