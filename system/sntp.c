#define _GNU_SOURCE
#include <arpa/inet.h>
#include <assert.h>
#include <netdb.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

/* This is the way to make TOSTRING(__LINE__) to work. */
#define STRINGIFY(x) #x
#define TOSTRING(x) STRINGIFY(x)

#define CHECK(x)                                     \
  if ((size_t)(x) == (size_t)(-1)) {                 \
    perror(__FILE__ ":" TOSTRING(__LINE__) ": " #x); \
    exit(1);                                         \
  }

int PORT = 123;
unsigned char data[15 * 4];

const long long NTP2UNIX = (70 * 365 + 17) * 86400LL;

void sigalrm_handler(int sig) {
  (void)sig;
  puts("Could not get time for some reason, exiting.");
  exit(1);
}

int main(void) {
  if (signal(SIGALRM, sigalrm_handler) == SIG_ERR) abort();
  alarm(3);

  int fd;
  struct sockaddr_in sa;

  data[0] = 19;

  CHECK(fd = socket(AF_INET, SOCK_DGRAM, 0));

  sa.sin_family = AF_INET;
  sa.sin_port = htons(PORT);
  inet_pton(AF_INET, "89.234.64.77", &sa.sin_addr);

  CHECK(sendto(fd, data, sizeof data, 0, (struct sockaddr*)&sa, sizeof sa));
  CHECK(read(fd, data, sizeof data));
  CHECK(close(fd));

  long long t = 0;
  t |= ((long long)data[40]) << 24;
  t |= ((long long)data[41]) << 16;
  t |= ((long long)data[42]) << 8;
  t |= ((long long)data[43]);
  t -= NTP2UNIX;
  t += 2;
  struct timeval tv;
  tv.tv_sec = t;
  tv.tv_usec = 0;
  CHECK(settimeofday(&tv, NULL));

  time_t curtime = time(NULL);
  char buf[4096];
  strftime(buf, 4096, "Time set to %Y/%m/%d %H:%M:%S.", localtime(&curtime));
  puts(buf);

  return 0;
}
