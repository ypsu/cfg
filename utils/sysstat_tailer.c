#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/file.h>
#include <sys/inotify.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define CHECK(cond) \
	do { \
		if (!(cond)) { \
			check(#cond, __FILE__, __func__, __LINE__); \
		} \
	} while(0)
static void
check(const char *expr, const char *file, const char *func, int line)
{
	const char *fmt;
	fmt = "check \"%s\" in %s at %s:%d failed, errno = %d (%m)\n";
	printf(fmt, expr, func, file, line, errno);
	abort();
}

int main(void)
{
	setlinebuf(stdout);
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
	struct inotify_event ev;
	int r;
	do {
		char buf[220];
		CHECK(flock(sysstat_fd, LOCK_SH) == 0);
		r = pread(sysstat_fd, buf, 200, 0);
		CHECK(flock(sysstat_fd, LOCK_UN) == 0);
		CHECK(r != -1);
		buf[r] = 0;
		CHECK(puts(buf) >= 0);
	} while ((r = read(inotify_fd, &ev, sizeof ev)) == sizeof ev);
	CHECK(r == sizeof ev);
	return 0;
}