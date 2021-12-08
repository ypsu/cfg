#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/inotify.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

#define CHECK(cond)                               \
  do {                                            \
    if (!(cond)) {                                \
      check(#cond, __FILE__, __func__, __LINE__); \
    }                                             \
  } while (0)
static void check(const char *expr, const char *file, const char *func,
                  int line) {
  const char *fmt;
  fmt = "check \"%s\" in %s at %s:%d failed, errno = %d (%m)\n";
  printf(fmt, expr, func, file, line, errno);
  abort();
}

const char usage[] =
    "sysstat_rewrite [OPTIONS...]\n"
    "Dumps /tmp/.sysstat whenever it changes.\n"
    "\n"
    "-h  Print this message.\n"
    "-r  Rewrite the timedate to the current time.\n";

int main(int argc, char **argv) {
  setlinebuf(stdout);
  bool rewrite_date = false;
  int opt;
  while ((opt = getopt(argc, argv, "hr")) != -1) {
    switch (opt) {
      case 'h':
        fputs(usage, stdout);
        exit(0);
      case 'r':
        rewrite_date = true;
        break;
      default:
        CHECK(false);
    }
  }
  int sysstat_fd = -1;
  while (sysstat_fd == -1) {
    sysstat_fd = open("/tmp/.sysstat", O_RDONLY);
    CHECK(sysstat_fd >= 0 || errno == ENOENT);
    if (sysstat_fd == -1) {
      puts("Waiting for sysstat to start up.");
      sleep(2);
    }
  }
  int inotify_fd = inotify_init();
  CHECK(inotify_fd != -1);
  int watchid = inotify_add_watch(inotify_fd, "/tmp/.sysstat", IN_MODIFY);
  CHECK(watchid != -1);

  int fdflags = fcntl(inotify_fd, F_GETFL, 0);
  CHECK(fdflags >= 0);
  int blockmode = fdflags;
  int nonblockmode = fdflags | O_NONBLOCK;

  struct inotify_event ev;
  int r;
  do {
    char buf[220];
    CHECK(flock(sysstat_fd, LOCK_SH) == 0);
    r = pread(sysstat_fd, buf, 200, 0);
    CHECK(flock(sysstat_fd, LOCK_UN) == 0);
    CHECK(r != -1);
    buf[r] = 0;
    int len = strlen(buf);
    if (rewrite_date && len > 20) {
      time_t t = time(NULL);
      struct tm *tm = localtime(&t);
      CHECK(snprintf(buf + len - 16, 17, "%04d-%02d-%02d %02d:%02d",
                     tm->tm_year + 1900, tm->tm_mon + 1, tm->tm_mday,
                     tm->tm_hour, tm->tm_min) < 17);
    }
    CHECK(puts(buf) >= 0);

    // drain inotify fd now to ignore duplicate events
    // and to ensure the read call in the while below blocks.
    CHECK(fcntl(inotify_fd, F_SETFL, nonblockmode) == 0);
    while (read(inotify_fd, &ev, sizeof(ev)) > 0) {
    }
    CHECK(fcntl(inotify_fd, F_SETFL, blockmode) == 0);
  } while ((r = read(inotify_fd, &ev, sizeof(ev))) == sizeof(ev));
  CHECK(r == sizeof ev);
  return 0;
}
