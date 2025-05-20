// pararun - parallel run. see usage below on what it does. pararun's output is
// equivalent to running the jobs in sequence so it has to buffer the output of
// the some jobs. it does this via a trick: pararun itself doesn't have any
// buffers at all. for each command pararun creates a pipe where the command
// writes its output. then pararun just prints each pipe in sequence. in other
// words the buffers exist in the kernel itself.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/signalfd.h>
#include <sys/sysinfo.h>
#include <sys/wait.h>
#include <unistd.h>

const char usage[] =
  "pararun - parallel run\n"
  "usage: pararun [flags] [prefix]\n"
  "pararun reads commands from stdin and runs them in parallel. one command\n"
  "per line. pararun buffers the outputs so that the command is equivalent\n"
  "of running the commands in sequence. the return code is the maximum among\n"
  "all the runs. if all returned successfully then it is 0. flags:\n"
  "  -h     : this help text\n"
  "  -j[num]: maximum number of threads to use. defaults to 2 times the\n"
  "           number of cores the computer has.\n"
  "  -q     : omit printing the commands (omit printing the yellow text).\n"
  "examples:\n"
  "parallel wordcount:\n"
  "  ls | pararun wc\n"
  "to download bunch of files:\n"
  "  pararun -j99 <urls.txt wget\n"
  "to compile bunch of files:\n"
  "  for f in *.c; do echo gcc -o ${f%.c} $f; done | pararun -q\n";

// check is like an assert but always enabled.
#define check(cond) checkfunc(cond, #cond, __FILE__, __LINE__)
void checkfunc(bool ok, const char *s, const char *file, int line) {
  if (ok) return;
  printf("checkfail at %s %d %s\n", file, line, s);
  if (errno != 0) printf("errno: %m\n");
  exit(1);
}

enum { maxargs = 999 };
enum { maxline = 99999 };
enum { maxthreads = 9999 };

static struct {
  // prefixargs represents the number of arguments passed in on the command line
  // argument.
  int prefixargs;

  // args holds the arguments. pararun passes this to execvp in the forked
  // children.
  char *args[maxargs + 1];

  // threadscount represent the number of children to run simultaneously. this
  // is the same as the -j argument.
  int threadscount;

  // quietmode corresponds to the -q parameter.
  bool quietmode;

  // inbuf holds the current input line.
  char inbuf[maxline + 1];

  // currentthread is the thread index at the head of all threads. this is the
  // thread whose pipe's output goes to stdout at the moment.
  int currentthread;

  // nextthread should be next thread index.
  int nextthread;

  // runningthreads represents the number of threads running currently.
  int runningthreads;

  // pipes[i % maxthreads] holds the read end of the running thread of the ith
  // command (starting from 0).
  int pipes[maxthreads];

  // started and finished means the number of threads started and finished. only
  // used for diagnostics.
  int started;
  int finished;
} g;

int main(int argc, char **argv) {
  // initalize globals, process cmdline flags.
  if (isatty(0)) {
    fputs(usage, stdout);
    exit(0);
  }
  g.threadscount = 2 * get_nprocs();
  argc--;
  argv++;
  while (argc >= 1) {
    if (strcmp(argv[0], "-h") == 0) {
      fputs(usage, stdout);
      exit(0);
    }
    if (strncmp(argv[0], "-j", 2) == 0) {
      g.threadscount = atoi(argv[0] + 2);
      if (g.threadscount < 1 || maxthreads < g.threadscount) {
        printf("bad argument to -j. must be between 1 and %d.\n", maxthreads);
        exit(1);
      }
      argc--;
      argv++;
      continue;
    }
    if (strcmp(argv[0], "-q") == 0) {
      g.quietmode = true;
      argc--;
      argv++;
      continue;
    }
    break;
  }
  g.prefixargs = argc;
  int prefixlen = 0;
  check(0 <= g.prefixargs && g.prefixargs <= maxargs);
  for (int i = 0; i < argc; i++) {
    g.args[i] = argv[i];
    prefixlen += strlen(argv[i]);
  }
  check(prefixlen <= maxline);
  prctl(PR_SET_PDEATHSIG, SIGTERM);

  // close stdin on exec so children cannot accidentally consume the commands
  // from stdin.
  int fdflags = fcntl(0, F_GETFD);
  check(fdflags != -1);
  check(fcntl(0, F_SETFD, fdflags | FD_CLOEXEC) == 0);

  // set up the signalfd for handling the sigchld signals.
  sigset_t sigmask;
  sigemptyset(&sigmask);
  sigaddset(&sigmask, SIGCHLD);
  check(sigprocmask(SIG_BLOCK, &sigmask, NULL) == 0);
  int sigfd = signalfd(-1, &sigmask, SFD_CLOEXEC);
  check(sigfd != -1);

  // run the main loop.
  int returncode = 0;
  g.currentthread = -1;
  bool neednewline = false;
  while (true) {
    // start the threads.
    while (feof(stdin) == 0 && g.runningthreads < g.threadscount) {
      if (fgets(g.inbuf, maxline + 1, stdin) == NULL) break;
      if (g.runningthreads > 0 && g.nextthread == g.currentthread) break;
      int linelen = strlen(g.inbuf);
      if (linelen == 0 || g.inbuf[linelen - 1] != '\n') {
        if (linelen == maxline) {
          puts("input error. too long input line?");
        } else {
          puts("input error. missing newline?");
        }
        printf("bad line: %s\n", g.inbuf);
        exit(1);
      }
      // set up cmdline arguments for the child task.
      g.inbuf[--linelen] = 0;
      char *tok = strtok(g.inbuf, " ");
      int a = g.prefixargs;
      do {
        if (a == maxargs) {
          printf("too many arguments for a command.");
          exit(1);
        }
        g.args[a++] = tok;
      } while ((tok = strtok(NULL, " ")) != NULL);
      g.args[a] = NULL;
      // set up output redirection for the child task.
      int pipefds[2];
      check(pipe2(pipefds, 0) == 0);
      int readfd = pipefds[0];
      int writefd = pipefds[1];
      int flags;
      check((flags = fcntl(readfd, F_GETFD)) != -1);
      check(fcntl(readfd, F_SETFD, flags | FD_CLOEXEC) != -1);
      g.pipes[g.nextthread] = readfd;
      // start the child task.
      g.started++;
      int chpid = fork();
      if (chpid == -1) {
        printf("could not fork: %m\n");
        exit(1);
      }
      if (chpid == 0) {
        check(close(1) == 0);
        check(close(2) == 0);
        check(dup2(writefd, 1) == 1);
        check(dup2(writefd, 2) == 2);
        check(close(writefd) == 0);
        if (!g.quietmode) {
          printf("\e[33m");
          for (int i = 0; i < a; i++) {
            printf("%s ", g.args[i]);
          }
          puts("\e[0m");
          fflush(stdout);
        }
        execvp(g.args[0], g.args);
        printf("execvp failed: %m\n");
        exit(1);
      } else {
        check(close(writefd) == 0);
      }
      g.nextthread = (g.nextthread + 1) % maxthreads;
      g.runningthreads++;
    }

    // process the sigchld and the read events.
    if (g.currentthread == -1) g.currentthread = 0;
    if (g.runningthreads == 0 && g.currentthread == g.nextthread) break;
    bool waitpipe = g.currentthread != g.nextthread;
    struct pollfd pfds[2];
    pfds[0].fd = sigfd;
    pfds[0].events = POLLIN;
    if (waitpipe) {
      pfds[1].fd = g.pipes[g.currentthread];
      pfds[1].events = POLLIN;
    }
    check(poll(pfds, waitpipe ? 2 : 1, -1) >= 0);
    if ((pfds[0].revents & POLLIN) != 0) {
      struct signalfd_siginfo sfdsi;
      check(read(sigfd, &sfdsi, sizeof(sfdsi)) == sizeof(sfdsi));
      check(sfdsi.ssi_signo == SIGCHLD);
      int wstatus;
      while (waitpid(-1, &wstatus, WNOHANG) > 0) {
        g.finished++;
        g.runningthreads--;
        if (WIFEXITED(wstatus)) {
          if (WEXITSTATUS(wstatus) > returncode) {
            returncode = WEXITSTATUS(wstatus);
          }
        } else if (returncode == 0) {
          returncode = 1;
        }
      }
      if (!g.quietmode && !neednewline) {
        const char fmt[] = "\r\e[K\e[33m%d/%d done\e[0m";
        int len = sprintf(g.inbuf, fmt, g.finished, g.started);
        check(write(1, g.inbuf, len) == len);
      }
    }
    if (waitpipe && (pfds[1].revents & POLLIN) != 0) {
      int len = 0;
      if (!g.quietmode && !neednewline) {
        len += sprintf(g.inbuf, "\r\e[K");
      }
      int rby;
      rby = read(g.pipes[g.currentthread], g.inbuf + len, maxline - 50);
      check(rby > 0);
      len += rby;
      neednewline = g.inbuf[len - 1] != '\n';
      if (!g.quietmode && !neednewline) {
        const char fmt[] = "\e[33m%d/%d done\e[0m";
        len += sprintf(g.inbuf + len, fmt, g.finished, g.started);
      }
      check(write(1, g.inbuf, len) == len);
    } else if (waitpipe && (pfds[1].revents & POLLHUP) != 0) {
      check(close(g.pipes[g.currentthread]) == 0);
      g.currentthread = (g.currentthread + 1) % maxthreads;
      if (!g.quietmode) {
        int len = 0;
        if (!neednewline) {
          len += sprintf(g.inbuf, "\r\e[K");
        } else {
          g.inbuf[len++] = '\n';
          neednewline = false;
        }
        check(write(1, g.inbuf, len) == len);
      }
    }
  }
  if (!g.quietmode && !neednewline) {
    int len = sprintf(g.inbuf, "\r\e[K");
    check(write(1, g.inbuf, len) == len);
  }
  check(feof(stdin) != 0);
  return returncode;
}
