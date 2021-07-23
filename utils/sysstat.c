#define _GNU_SOURCE
#include <alsa/asoundlib.h>
#include <bsd/string.h>
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/statvfs.h>
#include <sys/time.h>
#include <sys/utsname.h>
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

struct state {
  // The timepoint when this state was acquired.
  time_t date;
  struct timespec time;

  // Memory in bytes.
  int64_t mem_avail;

  // The data transferred in bytes since startup.
  int64_t net_down;
  int64_t net_up;

  // CPU usage in ticks since startup.
  int64_t cpu_used;
  int64_t cpu_all;

  // Volume between 0 and 100.
  int volume;

  // Battery level between 0 an 100.
  int battery;
};

enum { MAX_IFACES = 4 };

struct config {
  // Command line arguments.
  bool print_usage;
  bool daemonize;
  int delay_ms;
  const char *audio;
  const char *net_ifaces;
  const char *output;

  // Runtime data.
  int output_fd;
  int stat_fd;
  int mem_fd;
  int ifaces;
  int up_fd[MAX_IFACES];
  int down_fd[MAX_IFACES];
  struct utsname uname;
  snd_mixer_t *snd_mixer;
  snd_mixer_elem_t *snd_elem;
  int bat_full, bat_now;
};

static void sighup_handler(int sig) { (void)sig; }

// buf must be at least 9 bytes long.
static void fmt_bytes(int64_t bytes, char *buf, int min_unit) {
  enum { MAX_UNITS = 7 };
  static const char units[MAX_UNITS][4] = {
      "b", "k", "m", "g", "t", "p", "e",
  };
  int unit = 0;
  while (unit < min_unit || (bytes > 9000 && unit < MAX_UNITS)) {
    unit += 1;
    bytes += 1023;
    bytes /= 1024;
  }
  CHECK(snprintf(buf, 9, "%4lld%s", (long long)bytes, units[unit]) < 9);
}

// If a file contains only one positive number, this extracts that.
static int64_t extract_number(int fd) {
  long long val = 0;
  static char buf[32];
  CHECK(pread(fd, buf, 31, 0) >= 1);
  sscanf(buf, "%lld", &val);
  return val;
}

static const char usage[] =
    "Usage: sysstat [OPTION]...\n"
    "Start up the system stats collector.\n"
    "\n"
    "-a DEV     Use DEV alsa device for volume control. Default is Master.\n"
    "           Set to none if not needed.\n"
    "-d MSECS   Wait MSECS milliseconds between updates. The default is 1000 "
    "ms.\n"
    "-f         Stay in foreground instead of daemonizing.\n"
    "-h         Show this help.\n"
    "-n IFACES  Watch the comma separated IFACES for network stats. The "
    "default\n"
    "           is \"eth0\".\n"
    "-o FILE    Write human readable stats to FILE. \"-\" means stdout. The\n"
    "           default is \"/tmp/.sysstat\".\n";

int main(int argc, char **argv) {
  // Initial configuration.
  struct config config;
  config.print_usage = false;
  config.daemonize = true;
  config.delay_ms = 1000;
  config.audio = "Master";
  config.net_ifaces = "eth0";
  config.output = "/tmp/.sysstat";
  int opt;
  while ((opt = getopt(argc, argv, "a:d:fhn:o:")) != -1) {
    switch (opt) {
      case 'a':
        config.audio = optarg;
        break;
      case 'd':
        config.delay_ms = atoi(optarg);
        break;
      case 'f':
        config.daemonize = false;
        break;
      case 'h':
        config.print_usage = true;
        break;
      case 'n':
        config.net_ifaces = optarg;
        break;
      case 'o':
        config.output = optarg;
        break;
    }
  }
  if (config.delay_ms == 0) {
    puts("Error parsing the -d argument.");
    exit(1);
  }
  if (strcmp(config.output, "-") == 0 && config.daemonize) {
    puts("Can't daemonize and print to stdout at the same time.");
    exit(1);
  }
  if (config.print_usage) {
    puts(usage);
    exit(0);
  }

  // Daemonize ourselves.
  if (config.daemonize) {
    pid_t pid;
    CHECK((pid = fork()) != -1);
    if (pid > 0) {
      exit(0);
    }
    umask(0);
    CHECK(setsid() != -1);
    CHECK(chdir("/") == 0);
    CHECK(close(0) == 0);
    CHECK(close(1) == 0);
    CHECK(close(2) == 0);
  }

  // Set up the runtime data.
  CHECK(signal(SIGHUP, sighup_handler) != SIG_ERR);
  CHECK(signal(SIGUSR1, sighup_handler) != SIG_ERR);
  if (strcmp(config.output, "-") != 0) {
    int flags = O_WRONLY | O_CREAT | O_TRUNC;
    config.output_fd = open(config.output, flags, 0666);
  } else {
    config.output_fd = 1;
  }
  CHECK(config.output_fd != -1);
  if (config.daemonize) {
    // If we are a daemon, ensure that output_fd is STDOUT so errors
    // will be printed into this file.
    CHECK(config.output_fd == 0);
    CHECK((config.output_fd = dup(config.output_fd)) == 1);
    CHECK(close(0) == 0);
  }
  CHECK((config.stat_fd = open("/proc/stat", O_RDONLY)) != -1);
  CHECK((config.mem_fd = open("/proc/meminfo", O_RDONLY)) != -1);
  CHECK(uname(&config.uname) == 0);
  char ifaces[200];
  CHECK(strlcpy(ifaces, config.net_ifaces, 150) < 100);
  config.ifaces = 0;
  const char *iface = strtok(ifaces, ",");
  do {
    char f[256];
    const char *fmt;
    fmt = "/sys/class/net/%s/statistics/rx_bytes";
    snprintf(f, 200, fmt, iface);
    CHECK((config.down_fd[config.ifaces] = open(f, O_RDONLY)) > 0);
    fmt = "/sys/class/net/%s/statistics/tx_bytes";
    snprintf(f, 200, fmt, iface);
    CHECK((config.up_fd[config.ifaces] = open(f, O_RDONLY)) > 0);
    config.ifaces += 1;
  } while ((iface = strtok(NULL, ",")) != NULL);
  if (strcmp(config.audio, "none") != 0) {
    CHECK(snd_mixer_open(&config.snd_mixer, 0) == 0);
    snd_mixer_t *mixer = config.snd_mixer;
    snd_mixer_selem_id_t *sid;
    CHECK(snd_mixer_attach(mixer, "default") == 0);
    CHECK(snd_mixer_selem_register(mixer, NULL, NULL) == 0);
    CHECK(snd_mixer_load(mixer) == 0);
    CHECK(snd_mixer_selem_id_malloc(&sid) == 0);
    snd_mixer_selem_id_set_index(sid, 0);
    snd_mixer_selem_id_set_name(sid, config.audio);
    config.snd_elem = snd_mixer_find_selem(mixer, sid);
    CHECK(config.snd_elem != NULL);
  } else {
    config.snd_mixer = NULL;
    config.snd_elem = NULL;
  }
  int f = O_RDONLY;
  config.bat_full = open("/sys/class/power_supply/BAT0/energy_full", f);
  config.bat_now = open("/sys/class/power_supply/BAT0/energy_now", f);
  char hostname[32] = {};
  CHECK(gethostname(hostname, 31) == 0);

  // Main loop.
  struct state state;
  memset(&state, 0, sizeof state);
  state.volume = -1;
  int last_len = 0;
  while (true) {
    struct state ns;
    enum { BS = 4096 };
    char buf[BS + 1];
    ssize_t rby;

    // Read date/time.
    ns.date = time(NULL);
    CHECK(clock_gettime(CLOCK_MONOTONIC_RAW, &ns.time) == 0);

    // Read the CPU stats.
    {
      CHECK((rby = pread(config.stat_fd, buf, BS, 0)) > 10);
      long long user, nice, system, idle, io;
      const char fmt[] = "cpu %lld %lld %lld %lld %lld";
      sscanf(buf, fmt, &user, &nice, &system, &idle, &io);
      ns.cpu_used = user + nice + system + io;
      ns.cpu_all = ns.cpu_used + idle;
    }

    // Read the memory stats.
    CHECK((rby = pread(config.mem_fd, buf, BS, 0)) > 10);
    enum { MEMINFO_LINE_LENGTH = 28 };
    const char *p;
    long long avail;
    p = buf + 2 * MEMINFO_LINE_LENGTH;
    CHECK(sscanf(p, "MemAvailable: %lld", &avail) == 1);
    ns.mem_avail = avail * 1024;

    // Read the network stats.
    ns.net_up = 0;
    ns.net_down = 0;
    for (int i = 0; i < config.ifaces; i++) {
      ns.net_up += extract_number(config.up_fd[i]);
      ns.net_down += extract_number(config.down_fd[i]);
    }

    // Read the volume.
    ns.volume = state.volume;
    if (config.snd_mixer != NULL) {
      int evcnt = snd_mixer_handle_events(config.snd_mixer);
      CHECK(evcnt >= 0);
      if (evcnt == 0 && state.volume != -1) {
        goto volume_done;
      }
      snd_mixer_elem_t *e = config.snd_elem;
      long mn = 0, mx = 100, val;
      int r;
      r = snd_mixer_selem_get_playback_dB_range(e, &mn, &mx);
      if (r != 0) {
        // Fallback method.
        const char *c;
        c = "amixer -M get Master | grep -o '[0-9]*%'";
        FILE *f = popen(c, "r");
        CHECK(f != NULL);
        CHECK(fscanf(f, "%d", &ns.volume) == 1);
        fclose(f);
        goto volume_done;
      }
      snd_mixer_selem_channel_id_t ch = SND_MIXER_SCHN_MONO;
      r = snd_mixer_selem_get_playback_dB(e, ch, &val);
      CHECK(r == 0);
      double a = exp10((val - mx) / 6000.0);
      double b = exp10((mn - mx) / 6000.0);
      double v = (a - b) / (1.0 - b);
      ns.volume = lrint(v * 100.0);
    volume_done:;
    }

    // Read the battery data.
    char bat[16] = {};
    if (config.bat_now != -1 && config.bat_full != -1) {
      int64_t full = extract_number(config.bat_full);
      int64_t now = extract_number(config.bat_now);
      ns.battery = now * 100 / full;
      snprintf(bat, 16, "%3d%% bat ", ns.battery);
    }

    // Format the volume level.
    char vol[16] = {};
    if (ns.volume != -1) snprintf(vol, 16, "%3d%% vol ", ns.volume);

    // Print the stats.
    char mem[10], up[10], down[10];
    int cpu;
    struct tm *tm;
    double elapsed_time;
    elapsed_time = ns.time.tv_sec - state.time.tv_sec;
    elapsed_time += (ns.time.tv_nsec - state.time.tv_nsec) / 1.0e9;
    fmt_bytes(ns.mem_avail, mem, 2);
    int64_t up_bytes = ns.net_up - state.net_up;
    int64_t down_bytes = ns.net_down - state.net_down;
    fmt_bytes(llrint(up_bytes / elapsed_time), up, 1);
    fmt_bytes(llrint(down_bytes / elapsed_time), down, 1);
    double cpu_used = ns.cpu_used - state.cpu_used;
    double cpu_all = ns.cpu_all - state.cpu_all;
    cpu = lrint(cpu_used * 100.0 / cpu_all);
    tm = localtime(&ns.date);
    snprintf(buf, BS,
             "[%s] "
             "%s%s"
             "%5s mem "
             "%5s ↑ %5s ↓ "
             "%3d%% cpu "
             "%04d-%02d-%02d %02d:%02d",
             hostname, bat, vol, mem, up, down, cpu, tm->tm_year + 1900,
             tm->tm_mon + 1, tm->tm_mday, tm->tm_hour, tm->tm_min);
    int len = strlen(buf);
    if (strcmp(config.output, "-") != 0) {
      int r;
      while ((r = flock(config.output_fd, LOCK_EX)) == EINTR) {
      }
      CHECK(r == 0);
      CHECK(pwrite(config.output_fd, buf, len, 0) == len);
      if (len != last_len) {
        CHECK(ftruncate(config.output_fd, len) == 0);
        last_len = len;
      }
      CHECK(flock(config.output_fd, LOCK_UN) == 0);
    } else {
      strcat(buf, "\n");
      len += 1;
      CHECK(write(config.output_fd, buf, len) == len);
    }

    // Prepare for the next iteration.
    state = ns;
    usleep(config.delay_ms * 1000);
  }

  return 0;
}
