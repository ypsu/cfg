// this is just a silly child lock daemon for my machine.

#define _GNU_SOURCE
#include <X11/Xcursor/Xcursor.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/extensions/scrnsaver.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/signalfd.h>
#include <time.h>
#include <unistd.h>

// check is like an assert but always enabled.
#define check(cond) checkfunc(cond, #cond, __FILE__, __LINE__)
void checkfunc(bool ok, const char *s, const char *file, int line) {
  if (ok) return;
  printf("checkfail at %s %d %s\n", file, line, s);
  if (errno != 0) printf("errno: %m\n");
  exit(1);
}

int errhandler(Display *dpy, XErrorEvent *ev) {
  char buf[100];
  XGetErrorText(dpy, ev->error_code, buf, 100);
  printf("xlib error: %s\n", buf);
  exit(1);
}

Display *dpy;
Window root, window;
Cursor cursor;

void lock(void) {
  XMapRaised(dpy, window);

  int status = -1;
  for (int i = 0; status != GrabSuccess && i < 10; i++) {
    usleep(50000);
    status = XGrabPointer(dpy, root, False, 0, GrabModeAsync, GrabModeAsync,
                          None, cursor, CurrentTime);
  }
  if (status != GrabSuccess) {
    XUnmapWindow(dpy, window);
    XFlush(dpy);
    return;
  }
  status = -1;
  for (int i = 0; status != GrabSuccess && i < 10; i++) {
    usleep(50000);
    status = XGrabKeyboard(dpy, root, True, GrabModeAsync, GrabModeAsync,
                           CurrentTime);
  }
  if (status != GrabSuccess) {
    XUngrabPointer(dpy, CurrentTime);
    XUnmapWindow(dpy, window);
    XFlush(dpy);
    return;
  }

  XEvent ev;
  char pw[] = "qwwwwq";
  int pwlen = strlen(pw);
  int match = 0;
  while (XNextEvent(dpy, &ev) == 0) {
    if (ev.type != KeyPress && ev.type != KeyRelease) {
      XRaiseWindow(dpy, window);
      continue;
    }
    KeySym ksym;
    XLookupString(&ev.xkey, 0, 0, &ksym, 0);
    if ((int)ksym != pw[match]) match = 0;
    if ((int)ksym == pw[match]) match++;
    if (match == pwlen) {
      if (ev.type == KeyRelease) break;
      match = 0;
    }
  }

  XUngrabKeyboard(dpy, CurrentTime);
  XUngrabPointer(dpy, CurrentTime);
  XUnmapWindow(dpy, window);
  XDefineCursor(dpy, root, cursor);
  XFlush(dpy);
}

int main(int argc, char **argv) {
  if (argc != 2) {
    puts("usage: x11lock [start|stop|activate]");
    puts("");
    puts("starts a daemon in the background that locks x11");
    puts("whenever the x screensaver started.");
    puts("adjust that timeout with xset.");
    puts("");
    puts("activate: activates the lock.");
    puts("start: starts the background daemon.");
    puts("stop: stops the background daemon.");
    return 0;
  }

  // find a running instance if there's one.
  int mypid = getpid(), daemonpid = -1;
  char exename[100];
  int n;
  check((n = readlink("/proc/self/exe", exename, sizeof(exename) - 1)) > 0);
  exename[n] = 0;
  DIR *dirp = opendir("/proc");
  check(dirp != NULL);
  struct dirent *de;
  while (daemonpid == -1 && (de = readdir(dirp)) != NULL) {
    if (de->d_type != DT_DIR && de->d_type != DT_UNKNOWN) continue;
    int pid = -1;
    if (sscanf(de->d_name, "%d", &pid) != 1 || pid <= 1) continue;
    if (pid == mypid) continue;
    char name[30];
    sprintf(name, "/proc/%d/exe", pid);
    char buf[100];
    n = readlink(name, buf, sizeof(buf) - 1);
    if (n <= -1) continue;
    buf[n] = 0;
    if (strcmp(buf, exename) == 0) {
      daemonpid = pid;
    }
  }
  check(closedir(dirp) == 0);

  // process args.
  if (strcmp(argv[1], "activate") == 0) {
    if (daemonpid == -1) {
      puts("x11lock daemon not running, doing nothing.");
      return 1;
    }
    check(kill(daemonpid, SIGUSR1) == 0);
    return 0;
  }
  if (strcmp(argv[1], "stop") == 0) {
    if (daemonpid == -1) {
      puts("x11lock daemon not running, doing nothing.");
      return 1;
    }
    check(kill(daemonpid, SIGTERM) == 0);
    return 0;
  }
  if (strcmp(argv[1], "start") != 0) {
    puts("unrecognized arguments.");
    return 1;
  }
  if (daemonpid != -1) {
    puts("x11 already running, doing nothing.");
    return 0;
  }

  // daemonize.
  check(daemon(0, 0) == 0);

  // set up a signalfd for sigusr1.
  sigset_t ss;
  check(sigemptyset(&ss) == 0);
  check(sigaddset(&ss, SIGUSR1) == 0);
  check(sigprocmask(SIG_BLOCK, &ss, NULL) == 0);
  int sfd = signalfd(-1, &ss, 0);
  check(sfd != -1);

  // initialize x11 structures.
  XSetErrorHandler(errhandler);
  dpy = XOpenDisplay(NULL);
  check(dpy != NULL);
  int eventcode, unused;
  check(XScreenSaverQueryExtension(dpy, &eventcode, &unused));
  int xfd = ConnectionNumber(dpy);
  root = DefaultRootWindow(dpy);
  int screen = DefaultScreen(dpy);
  XSetWindowAttributes wa = {};
  wa.override_redirect = 1;
  window = XCreateWindow(
      dpy, root, 0, 0, DisplayWidth(dpy, screen), DisplayHeight(dpy, screen), 0,
      DefaultDepth(dpy, screen), CopyFromParent, DefaultVisual(dpy, screen),
      CWOverrideRedirect | CWBackPixel, &wa);
  XScreenSaverSelectInput(dpy, root, ScreenSaverNotifyMask);
  cursor = XcursorLibraryLoadCursor(dpy, "arrow");

  // main loop.
  while (true) {
    XFlush(dpy);
    struct pollfd ps[2] = {
        {.fd = sfd, .events = POLLIN},
        {.fd = xfd, .events = POLLIN},
    };
    poll(ps, 2, -1);
    if ((ps[0].revents & POLLIN) != 0) {
      struct signalfd_siginfo si;
      check(read(sfd, &si, sizeof(si)) == sizeof(si));
      if (si.ssi_signo == SIGUSR1) lock();
    }
    while (XPending(dpy) > 0) {
      XEvent ev;
      check(XNextEvent(dpy, &ev) == 0);
      if (ev.type != eventcode) continue;
      // give a chance to cancel the screensaver.
      sleep(10);
      XScreenSaverInfo info = {};
      check(XScreenSaverQueryInfo(dpy, root, &info) != 0);
      if (info.state == ScreenSaverOn) lock();
    }
  }
  return 0;
}
