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
	unlink(fname);

	int fd = socket(AF_UNIX, SOCK_DGRAM, 0);
	HANDLE_CASE(fd == -1);

	struct sockaddr_un addr;
	addr.sun_family = AF_UNIX;
	strcpy(addr.sun_path, fname);

	HANDLE_CASE(bind(fd, &addr, sizeof addr) == -1);

	char buf[4096];
	int sz;
	while ((sz = read(fd, buf, 4095)) != -1) {
		if (sz == 0)
			continue;
		if (sz == 1 && buf[0] >= '1' && buf[0] <= '4') {
			int idx = buf[0] - '0';
			if (idx < argc) {
				int len = strlen(argv[idx]);
				if (len < 4000) {
					memcpy(buf, argv[idx], len);
					sz = len;
				}
			}
		}
		buf[sz] = 0;
		printf("\e[H\e[J");
		puts(buf);
		int res = system(buf);
		if (res == 0)
			printf("\e[32mok (0)\e[0m\n");
		else
			printf("\e[31merror (%d)\e[0m\n", res);
	}

	return 0;
}
