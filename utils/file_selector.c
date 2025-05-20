#define _GNU_SOURCE
#include <assert.h>
#include <dirent.h>
#include <errno.h>
#include <poll.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <termios.h>
#include <unistd.h>

// this must come after stdio.h.
#include <readline/readline.h>

#define HANDLE_CASE(cond)                               \
  do {                                                  \
    if ((cond)) {                                       \
      handle_case(#cond, __FILE__, __func__, __LINE__); \
    }                                                   \
  } while (0);

void handle_case(const char *expr, const char *file, const char *func,
                 int line) {
  printf("unhandled case, errno = %d (%m)\n", errno);
  printf("in expression '%s'\n", expr);
  printf("in function %s\n", func);
  printf("in file %s\n", file);
  printf("at line %d\n", (int)line);
  exit(1);
}

void swrite(int fd, const void *data, int len) {
  HANDLE_CASE(write(fd, data, len) != len);
}

void swap_fd(int a, int b) {
  int t;
  HANDLE_CASE((t = dup(a)) == -1);
  HANDLE_CASE(close(a) != 0);
  HANDLE_CASE(dup(b) != a);
  HANDLE_CASE(close(b) != 0);
  HANDLE_CASE(dup(t) != b);
  HANDLE_CASE(close(t) != 0);
}

// 0 <- preprocessed input
// 1 <- stdout
// 2 <- stderr
// 4 <- fd for writing to preprocessed input
// 5 <- stdin
// The preprocessed input is used to handle the Escape key for early exit.
void setup_fd(void) {
  int p[2];
  HANDLE_CASE(pipe(p) != 0);
  HANDLE_CASE(dup(0) != 5);
  HANDLE_CASE(close(0) != 0);
  HANDLE_CASE(dup(3) != 0);
  HANDLE_CASE(close(3) != 0);
}

void reset_fd(void) {
  HANDLE_CASE(close(0) != 0);
  HANDLE_CASE(close(4) != 0);
  HANDLE_CASE(dup(5) != 0);
}

enum { BUFFER_MAX = 4 * 1024 * 1024 };
enum { ENTRIES_MAX = 32768 };
enum { OUTBUF_MAX = 131072 };

int term_width, term_height;
char output_buffer[OUTBUF_MAX];

void get_term_dimensions(void) {
  struct winsize winsize;
  ioctl(0, TIOCGWINSZ, &winsize);
  term_width = winsize.ws_col;
  term_height = winsize.ws_row;
  HANDLE_CASE(term_width * term_height >= OUTBUF_MAX);
}

int tolower_table[256];
bool isupper_table[256];

void tolower_table_calc(void) {
  for (int i = 0; i < 256; ++i) tolower_table[i] = i;
  for (int i = 'A'; i <= 'Z'; ++i) {
    isupper_table[i] = true;
    tolower_table[i] = i - 'A' + 'a';
  }
}

int buffer_sz;
char buffer[BUFFER_MAX];

char *bufdup(const char *buf, int sz) {
  HANDLE_CASE(buffer_sz + sz >= BUFFER_MAX);
  char *p = buffer + buffer_sz;
  memcpy(p, buf, sz);
  buffer_sz += sz;
  return p;
}

int entries_sz;
struct entry {
  int len;
  const char *name;
  const char *name_lower;
} entries[ENTRIES_MAX];

void entry_add(const char *buf, int len) {
  HANDLE_CASE(entries_sz >= ENTRIES_MAX);
  struct entry *e = &entries[entries_sz++];
  e->len = len;
  e->name = buf;
  char *lowbuf = bufdup(buf, len + 1);
  for (char *p = lowbuf; *p != 0; ++p) *p = tolower_table[(unsigned char)*p];
  e->name_lower = lowbuf;
}

int entry_cmp(const void *a, const void *b) {
  const struct entry *x = a;
  const struct entry *y = b;
  return strcmp(x->name_lower, y->name_lower);
}

int curpath_sz;
char curpath[PATH_MAX + 1];

void read_subtree(DIR *dirp) {
  struct dirent *ent;
  while ((ent = readdir(dirp)) != NULL) {
    if (ent->d_name[0] == '.') continue;
    int len = strlen(ent->d_name);
    HANDLE_CASE(curpath_sz + len + 1 >= PATH_MAX);

    int oldsz = curpath_sz;
    memcpy(curpath + curpath_sz, ent->d_name, len);
    curpath_sz += len;
    curpath[curpath_sz] = 0;

    if (ent->d_type == DT_UNKNOWN) {
      struct stat s;
      HANDLE_CASE(stat(curpath, &s) == -1);
      if (S_ISDIR(s.st_mode)) ent->d_type = DT_DIR;
    }

    if (ent->d_type == DT_DIR) {
      DIR *subdirp = opendir(curpath);
      if (subdirp == NULL) {
        HANDLE_CASE(errno != EACCES);
      } else {
        HANDLE_CASE(subdirp == NULL);
        curpath[curpath_sz++] = '/';
        read_subtree(subdirp);
        HANDLE_CASE(closedir(subdirp) == -1);
      }
    } else {
      entry_add(bufdup(curpath, curpath_sz + 1), curpath_sz);
    }

    curpath_sz = oldsz;
  }
}

void read_hierarchy(void) {
  puts("reading directory hierarchy");

  DIR *dirp = opendir(curpath);
  HANDLE_CASE(dirp == NULL);
  read_subtree(dirp);
  HANDLE_CASE(closedir(dirp) == -1);
}

int matches_count;
int selection;
int first_match;
void match_pattern(const char *pattern) {
  bool exact_first = false;
  bool exact_last = false;

  if (pattern[0] == '^') {
    exact_first = true;
    pattern += 1;
  }
  int patlen = strlen(pattern);
  if (patlen > 0 && pattern[patlen - 1] == '$') {
    exact_last = true;
  }

  char words[32][256];
  int words_cnt = 0;
  int offset = 0;
  while (words_cnt < 32) {
    int new_offset = 0;
    const char *s = pattern + offset;
    if (sscanf(s, "%255s%n", words[words_cnt], &new_offset) != 1) break;
    offset += new_offset;
    words_cnt += 1;
  }

  int last_len = 0;
  if (words_cnt > 0 && exact_last) {
    int lw_len = strlen(words[words_cnt - 1]);
    words[words_cnt - 1][lw_len - 1] = 0;
    last_len = lw_len - 1;
  }

restart:
  // We are doing exact matches except for capital letters. Capital
  // letters can be preceded by any other characters.
  first_match = -1;

  char *buf = output_buffer;
  memcpy(buf, "\e[s\e[J\n\n", 8);
  buf += 7;

  int matched = 0;
  for (int i = 0; i < entries_sz; ++i) {
    const struct entry *e = &entries[i];
    const char *q = e->name_lower;
    int qlen = e->len;
    int word;

    for (word = 0; word < words_cnt; ++word) {
      if (word == 0 && exact_first) {
        if (strstr(q, words[0]) != q) break;
      } else if (word == words_cnt - 1 && exact_last) {
        if (last_len > qlen) break;
        const char *qe = q + qlen - last_len;
        if (strcmp(qe, words[words_cnt - 1]) != 0) break;
      } else if (strstr(q, words[word]) == NULL) {
        break;
      }
    }

    if (word == words_cnt) {
      matched += 1;

      if (matched - 1 == selection) {
        first_match = i;
        memcpy(buf, " -> ", 4);
      } else {
        memcpy(buf, "    ", 4);
      }
      buf += 4;
      const char *s = e->name;
      if (matched >= term_height - 3) {
        s = "... and others ...";
      } else if (qlen + 6 >= term_width) {
        memcpy(buf, "...", 3);
        buf += 3;
        s = e->name + qlen - term_width + 8;
      }
      int slen = strlen(s);
      memcpy(buf, s, slen);
      buf += slen;
      *buf++ = '\n';
      if (matched >= term_height - 3) break;
    }
  }

  memcpy(buf, "\e[u", 3);
  buf += 3;
  swrite(1, output_buffer, buf - output_buffer);
  rl_refresh_line(0, 0);

  matches_count = matched;
  if (matched > 0 && selection >= matched) {
    selection = matched - 1;
    goto restart;
  }
}

void noop(char *s) { (void)s; }

bool streq(const char *a, const char *b) { return strcmp(a, b) == 0; }

int main(int argc, char **argv) {
  for (int fd = 3; fd < 16; ++fd) close(fd);

  curpath[0] = '.';

  int opt;
  while ((opt = getopt(argc, argv, "u:")) != -1) {
    if (opt == 'u') {
      int n = atoi(optarg);
      for (int i = 0; i < n; ++i) {
        curpath[curpath_sz++] = '.';
        curpath[curpath_sz++] = '.';
        curpath[curpath_sz++] = '/';
      }
    } else {
      puts("file-selector [-u n] [entries...]");
      puts("");
      puts("-u n: go up n levels in the directory hierarchy");
      exit(1);
    }
  }

  // Swap stdout with stderr so readline won't spam stdout where the
  // result will go.
  swap_fd(1, 2);
  swrite(1, "\e[?1049h", 8);
  swrite(1, "\e[H\e[2J", 7);

  get_term_dimensions();
  tolower_table_calc();

  if (optind == argc) {
    read_hierarchy();
  } else {
    for (int i = optind; i < argc; ++i) {
      entry_add(argv[i], strlen(argv[i]));
    }
  }
  qsort(entries, entries_sz, sizeof entries[0], entry_cmp);

  rl_callback_handler_install("fuzzy name: ", noop);
  setup_fd();

  swrite(1, "\e[H\e[2J", 7);
  match_pattern("");
  while (true) {
    char ch[8] = {};
    int rby = read(5, ch, 7);
    HANDLE_CASE(rby == -1);
    if (rby == 1 && ch[0] == 27 && rl_end != 0) {
      // Escape on nonempty string: clear pattern.
      rl_delete_text(0, rl_end);
      // Readline hack. Not sure why is this needed.
      rl_callback_handler_install("fuzzy name: ", noop);
      rby = 0;
    } else if (rby == 0 || (rby == 1 && ch[0] == 27)) {
      // Escape on empty string: quit,
      reset_fd();
      rl_deprep_terminal();
      swrite(1, "\e[?1049l", 8);
      exit(1);
    } else if (ch[0] == 13) {
      // Pressed Return.
      reset_fd();
      rl_deprep_terminal();
      swrite(1, "\e[?1049l", 8);
      if (first_match != -1) {
        fputs(entries[first_match].name, stderr);
        fputc('\n', stderr);
      } else {
        exit(1);
      }
      exit(0);
    } else if (ch[0] == 10 || ch[0] == 14 || streq(ch, "\e[B")) {
      // Pressed ^J or ^N or Down.
      if (matches_count > 0) {
        selection += 1;
        selection %= matches_count;
      }
      match_pattern(rl_line_buffer);
      continue;
    } else if (ch[0] == 11 || ch[0] == 16 || streq(ch, "\e[A")) {
      // Pressed ^K or ^P or Up.
      if (matches_count > 0) {
        selection += matches_count - 1;
        selection %= matches_count;
      }
      match_pattern(rl_line_buffer);
      continue;
    }

    for (int i = 0; i < rby; ++i) {
      swrite(4, ch + i, 1);
      rl_callback_read_char();
    }
    match_pattern(rl_line_buffer);
  }

  return 0;
}
