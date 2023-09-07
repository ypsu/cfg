// see https://iio.ie/enacc.
#define _GNU_SOURCE
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
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

enum { idlengthlimit = 15 };
enum { dimensionslimit = 10 };

struct {
  int limit;
  int dimensions;
  char dimensionid[dimensionslimit][idlengthlimit + 1];
  int dimensionvalue[dimensionslimit];
} g;

// returns true on success, false otherwise.
// processlineerror will contain the error message.
char processlineerror[120];
bool processline(char *id, int val) {
  if (strlen(id) > idlengthlimit) {
    const char fmt[] = "id '%s' too long. limit is %d chars.\n";
    sprintf(processlineerror, fmt, id, idlengthlimit);
    return false;
  }
  if (strcmp(id, "limit") == 0) {
    g.limit = val;
    return true;
  }
  if (g.limit == 0) {
    strcpy(processlineerror, "missing limit.");
    return false;
  }
  if (strcmp(id, "sub") == 0) {
    for (int i = 0; i < g.dimensions; i++) {
      if (g.dimensionvalue[i] != 0) g.dimensionvalue[i] -= val;
    }
    return true;
  }
  int dim;
  for (dim = 0; dim < g.dimensions; dim++) {
    if (strcmp(id, g.dimensionid[dim]) == 0) break;
  }
  if (dim == g.dimensions) {
    // add a new dimension.
    if (val != 0) {
      sprintf(processlineerror, "unknown id %s.", id);
      return false;
    }
    check(g.dimensions < dimensionslimit);
    strcpy(g.dimensionid[g.dimensions++], id);
    return true;
  }
  if (val == 0) {
    // reset and hide dimension.
    g.dimensionvalue[dim] = 0;
    return true;
  }
  g.dimensionvalue[dim] += val;
  // being at 0 means the dimension doesn't exist so avoid 0.
  if (g.dimensionvalue[dim] == 0) g.dimensionvalue[dim] = -1;
  if (g.dimensionvalue[dim] > g.limit) g.dimensionvalue[dim] = g.limit;
  return true;
}

int main(int argc, char **argv) {
  // open data file.
  check(chdir(getenv("HOME")) == 0);
  check(freopen(".enacc", "r", stdin));

  // process the data file.
  char allcomments[10000];
  char *commentsend = allcomments;
  char line[90];
  for (int lineidx = 1; fgets(line, 85, stdin) != NULL; lineidx++) {
    int len = strlen(line);
    if (len > 80) {
      fprintf(stderr, "line %d too long. contents:\n%s\n", lineidx, line);
      exit(1);
    }
    if (line[0] == '\n') continue;
    if (line[0] == '#') {
      check(commentsend + len < allcomments + sizeof(allcomments));
      strcpy(commentsend, line);
      commentsend += len;
      continue;
    }
    char id[90];
    int val;
    if (sscanf(line, "%*s %*s %s %d", id, &val) != 2) {
      fprintf(stderr, "line %d wrong format. contents: %s\n", lineidx, line);
      exit(1);
    }
    if (!processline(id, val)) {
      fprintf(stderr, "line %d: %s\n", lineidx, processlineerror);
      exit(1);
    }
  }

  // sanity check the command line arguments.
  if (argc >= 3 && argc % 2 != 1) {
    puts("invalid number of arguments.");
    exit(1);
  }
  if (argc <= 1) {
    puts("enacc - energy accounter.");
    puts("usage 1: enacc [dim val]...");
    puts("usage 2: enacc [val]");
    puts("dim is either 'sub' or one of the energy dimensions.");
    puts("dim 'sub' subtracts val from each energy dimension.");
    puts("otherwise val is added to the specific dimension.");
    puts("val on its own is the same as 'sub val'.");
    puts("~/.enacc file comments and energy levels:");
    fputs(allcomments, stdout);
  }

  // process the command line arguments.
  int argcnt = argc - 1;
  char **argstr = argv + 1;
  char *(tmp[2]);
  if (argc == 2) {
    // handle the "enacc [val]" case by treating it as "enacc sub [val]".
    argcnt = 2;
    tmp[0] = "sub";
    tmp[1] = argv[1];
    argstr = tmp;
  }
  for (int a = 0; a < argcnt; a += 2) {
    if (strlen(argstr[a]) > idlengthlimit) {
      fprintf(stderr, "id '%s' too long.\n", argstr[a]);
      exit(1);
    }
    int v;
    if (sscanf(argstr[a + 1], "%d", &v) != 1) {
      fprintf(stderr, "couldn't parse '%s'.\n", argstr[a + 1]);
      exit(1);
    }
    if (v <= 0) {
      fprintf(stderr, "values must be positive numbers.\n");
      exit(1);
    }
    if (v > g.limit) {
      fprintf(stderr, "value too big.\n");
      exit(1);
    }
    if (!processline(argstr[a], v)) {
      fprintf(stderr, "error: %s\n", processlineerror);
      exit(1);
    }
  }

  // print the new stats.
  for (int i = 0; i < g.dimensions; i++) {
    if (g.dimensionvalue[i] != 0) {
      printf("%s:%d ", g.dimensionid[i], g.dimensionvalue[i]);
    }
  }
  puts("");

  // append the command line arguments to the data file.
  char tmstr[60];
  time_t t = time(NULL);
  strftime(tmstr, 50, "%F %H:%M", localtime(&t));
  FILE *f = fopen(".enacc", "a");
  check(f != NULL);
  for (int a = 0; a < argcnt; a += 2) {
    fprintf(f, "%s %s %s\n", tmstr, argstr[a], argstr[a + 1]);
  }
  check(fclose(f) == 0);
  return 0;
}
