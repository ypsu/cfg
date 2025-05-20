#define _GNU_SOURCE
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <unistd.h>

char *mycmd;
char **myenvp;

// Allow re-execing ourselves with the -r command line argument in order to not
// hold handlers to deleted files (in case init was deleted) because then we
// can't unmount /.
void sigterm_handler(int signo) {
  (void)signo;
  char *argv[3] = {mycmd, "-r", NULL};
  execve(mycmd, argv, myenvp);
  abort();
}

int main(int argc, char **argv, char **envp) {
  if (getpid() != 1) {
    puts("This is the init. You shall not run it.");
    exit(1);
  }
  mycmd = argv[0];
  myenvp = envp;

  if (argc < 2 || strcmp(argv[1], "-r") != 0) {
    struct rlimit lim = {-1, -1};
    setrlimit(RLIMIT_MEMLOCK, &lim);
    int chpid = fork();
    if (chpid == -1) abort();
    if (chpid == 0) {
      const char *argv[] = {"/root/.sbin/boot.sh", NULL};
      const char *envp[] = {NULL};
      execve(argv[0], (char **)argv, (char **)envp);
      abort();
    }
  }

  for (int sig = 0; sig < 32; ++sig) {
    signal(sig, SIG_IGN);
  }
  signal(SIGTERM, sigterm_handler);
  while (true) {
    wait(NULL);
  }
  return 0;
}
