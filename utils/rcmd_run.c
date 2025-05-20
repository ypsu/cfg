#define _GNU_SOURCE
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#define HANDLE_CASE(cond) {if ((cond)) handle_case(#cond, __FILE__, __func__, __LINE__);}

void handle_case(const char *expr, const char *file, const char *func, int line)
{
	printf("unhandled case, errno = %d (%m)\n", errno);
	printf("in expression '%s'\n", expr);
	printf("in function %s\n", func);
	printf("in file %s\n", file);
	printf("at line %d\n", (int) line);
	exit(1);
}

const char fname[] = "/tmp/.rcmd_socket";

int main(int argc, char **argv)
{
	int fd = socket(AF_UNIX, SOCK_DGRAM, 0);
	HANDLE_CASE(fd == -1);

	struct sockaddr_un addr;
	addr.sun_family = AF_UNIX;
	strcpy(addr.sun_path, fname);

	HANDLE_CASE(connect(fd, &addr, sizeof addr) == -1);

	int sz = 0;
	char buf[4096];

	for (int i = 1; i < argc; ++i) {
		if (i != 1) {
			buf[sz] = ' ';
			sz += 1;
		}
		int asz = strlen(argv[i]);
		HANDLE_CASE(sz+asz >= 4096);
		memcpy(buf+sz, argv[i], asz);
		sz += asz;
	}

	HANDLE_CASE(write(fd, buf, sz) != sz);

	return 0;
}
