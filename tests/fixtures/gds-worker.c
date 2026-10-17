#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static volatile sig_atomic_t stop_requested = 0;

static void handle_signal(int signal_number) {
  (void)signal_number;
  stop_requested = 1;
}

int main(int argc, char **argv) {
  const char *status_file = NULL;
  FILE *output;
  int index;

  for (index = 1; index + 1 < argc; ++index) {
    if (strcmp(argv[index], "--status-file") == 0) {
      status_file = argv[index + 1];
      break;
    }
  }
  if (status_file == NULL) return 2;

  signal(SIGINT, handle_signal);
  signal(SIGTERM, handle_signal);
  signal(SIGHUP, handle_signal);
  output = fopen(status_file, "w");
  if (output == NULL) return 1;
  fprintf(output, "pid=%ld\n", (long)getpid());
  fprintf(output, "device=0\n");
  fprintf(output, "memory_percent=25\n");
  fprintf(output, "allocated_bytes=1024\n");
  fprintf(output, "total_bytes=4096\n");
  if (fclose(output) != 0) return 1;

  while (!stop_requested) usleep(10000);
  unlink(status_file);
  return 0;
}
