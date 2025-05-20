#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#define CHECK(cond) do {\
	if (!(cond)) {\
		check(#cond, __FILE__, __func__, __LINE__);\
	}\
} while (0);

void check(const char *expr, const char *file, const char *func, int line)
{
	printf("check failed: %s\n", expr);
	const char *fmt = "errno: %d (%m), function: %s at %s:%d\n";
	printf(fmt, errno, func, file, line);
	abort();
}

enum { MAX_PROCESSES = 256000 };

struct process {
	int pid, ppid;
	char *name;
} processes[MAX_PROCESSES];
int processes_count;

int main(void)
{
	int server_pid = -1;

	// Iterate over all processes via the /proc filesystem.
	DIR *dir = opendir("/proc/");
	CHECK(dir != NULL);
	struct dirent *ent;
	while (processes_count < MAX_PROCESSES && (ent=readdir(dir)) != NULL) {
		if (ent->d_type != DT_DIR)
			continue;

		// Determine pid from dirname.
		int pid = 0;
		for (int i = 0; pid >= 0 && ent->d_name[i] != 0; ++i) {
			char ch = ent->d_name[i];
			if (ch >= '0' && ch <= '9')
				pid = pid*10 + ch-'0';
			else {
				pid = -1;
			}
		}
		if (pid <= 1)
			continue;

		// Process the status file for the process.
		char statfilename[256];
		sprintf(statfilename, "/proc/%d/status", pid);
		char name[PATH_MAX+16];
		int ppid;
		FILE *f = fopen(statfilename, "r");
		if (f == NULL)
			continue;
		const char *fmt;
		fmt = "%*s %[^\n]%*s %*[^\n]%*s%*s %*s%*s %*s%*s %*s%d";
		CHECK(fscanf(f, fmt, name, &ppid) == 2);
		CHECK(fclose(f) == 0);

		// Save the data.
		struct process *p = &processes[processes_count++];
		p->pid = pid;
		p->ppid = ppid;
		p->name = strdup(name);
		if (strcmp(name, "rcmd_server") == 0) {
			if (server_pid != -1)
				fprintf(stderr, "Extra rcmd_server found!\n");
			server_pid = pid;
		}
	}
	CHECK(closedir(dir) == 0);

	// Kill the child of rcmd_server.
	if (server_pid == -1) {
		fprintf(stderr, "No rcmd_server found.\n");
		exit(1);
	}
	for (int i = 0; i < processes_count; ++i) {
		const struct process *p = &processes[i];
		if (p->ppid == server_pid)
			kill(p->pid, SIGINT);
	}
	return 0;
}
