#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <getopt.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

#define CHECK(x) check((x), #x, __FILE__, __LINE__);
static void check(bool succeeded, const char *str, const char *file, int line)
{
	if (succeeded)
		return;

	fprintf(stderr, "%s:%d: check failed: %s\n", file, line, str);
	fprintf(stderr, "errno = %m\n");
	exit(1);
}

static char filename[256] = "/var/log/sys.log";
static int write_limit = 64*1000*1000;
static char command[256] = "";

static void print_usage(void)
{
	puts("Usage: basic_syslogd [-o file] [-l limit] [-c cmd]");
	puts("");
	puts("You can send the application SIGUSR1 one to close the current");
	puts("logfile and reopen it. This is useful if you renamed the");
	puts("logfile but you want the logger continue logging to the file");
	puts("with the original name. The logfile is always opened in append");
	puts("mode");
	puts("");
	puts("-o file:");
	puts("  This is the output file. Default is /var/log/sys.log.");
	puts("");
	puts("-l limit:");
	puts("  Stop logging after [limit] megabytes. This is to avoid");
	puts("  filling up your drive in case something went wrong on your");
	puts("  system. SIGUSR1 will clear the internal counter for this");
	puts("  limit so after the signal basic_syslogd will log again to the");
	puts("  file.");
	puts("");
	puts("-c cmd:");
	puts("  Invoke cmd when we cross [limit/2] megabytes of output. You");
	puts("  can use this to notify yourself when the log is getting big.");
	puts("  If SIGUSR1 is received this counter will reset and cmd will");
	puts("  be called again if we cross [limit/2] again.");
}

static void process_args(int argc, char **argv)
{
	int ch;
	int arg;
	while ((ch = getopt(argc, argv, "ho:l:c:")) != -1) {
		switch (ch) {
		case 'h':
			print_usage();
			exit(0);
		case 'o':
			CHECK(strlen(optarg) < 250);
			strcpy(filename, optarg);
			break;
		case 'l':
			arg = atoi(optarg);
			CHECK(0 < arg && arg < 1024);
			write_limit = arg * 1000 * 1000;
			break;
		case 'c':
			CHECK(strlen(optarg) < 250);
			strcpy(command, optarg);
			break;
		default:
			exit(1);
		}
	}
}

static int create_devlog(void)
{
	unlink("/dev/log");
	int fd = socket(AF_UNIX, SOCK_DGRAM, 0);
	CHECK(fd != -1);
	struct sockaddr_un addr = {
		.sun_family = AF_UNIX,
		.sun_path = "/dev/log"
	};
	CHECK(bind(fd, (struct sockaddr *) &addr, sizeof addr) == 0);
	CHECK(chmod("/dev/log", 0777) == 0);
	return fd;
}

static int open_logfile(void)
{
	int fd = open(filename, O_WRONLY | O_APPEND | O_CREAT, 0644);
	CHECK(fd != -1);
	return fd;
}

static bool had_signal;
static void handle_sigusr1(int sig)
{
	CHECK(sig == SIGUSR1);
	had_signal = true;
}

static void setup_signal_handling(void)
{
	struct sigaction act = {
		.sa_handler = handle_sigusr1,
		.sa_flags = 0,
	};
	CHECK(sigemptyset(&act.sa_mask) == 0);
	CHECK(sigaction(SIGUSR1, &act, NULL) == 0);
}

int main(int argc, char **argv)
{
	process_args(argc, argv);
	setup_signal_handling();

	int ifd, ofd;
	ifd = create_devlog();
	ofd = open_logfile();
	int written = 0;

	while (true) {
		if (had_signal) {
			had_signal = false;
			CHECK(close(ofd) == 0);
			ofd = open_logfile();
			written = 0;
		}

		char buf[4096];
		int sz = read(ifd, buf, sizeof(buf)-1);
		if (sz == -1 && errno == EINTR)
			continue;
		CHECK(sz > 0);
		buf[sz++] = '\n';
		if (written < write_limit/2 && written+sz >= write_limit/2) {
			if (command[0] != 0)
				system(command);
		}
		if (written < write_limit) {
			written += sz;
			CHECK(write(ofd, buf, sz) == sz);
		}
	}

	return 0;
}
