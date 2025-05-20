#define _GNU_SOURCE
#include <assert.h>
#include <ctype.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define HANDLE_CASE(cond)                                       \
  do {                                                          \
    if (cond) handle_case(#cond, __FILE__, __func__, __LINE__); \
  } while (0)
void handle_case(const char *expr, const char *file, const char *fn, int ln) {
  printf("unhandled case, errno = %d (%m)\n", errno);
  printf("in expression '%s'\n", expr);
  printf("in function %s\n", fn);
  printf("in file %s\n", file);
  printf("at line %d\n", ln);
  abort();
}

#define MAXSTR 4096
const int WIDTH = 80;

#define MAX_BUFFER (1024 * 1024)
uint8_t input[MAX_BUFFER];
int input_length;
int lines;

uint8_t output[MAX_BUFFER];
int output_length;

uint8_t indent_str[MAXSTR];
int indent_length;

int cur_width;

void put(int ch) {
  assert(ch >= 0);
  HANDLE_CASE(output_length >= MAX_BUFFER);
  output[output_length++] = ch;
}

void add_newline(void) {
  put('\n');
  for (int i = 0; indent_str[i] != 0; ++i) put(indent_str[i]);
  cur_width = indent_length;
}

int word_start;
int word_fmtlength;
int word_datalength;

void find_next_word(void) {
  // Skip whitespace
  while (word_start < input_length && isspace(input[word_start]))
    word_start += 1;
  if (word_start >= input_length) return;

  // Find the end of the word
  bool in_tag = false;
  word_fmtlength = 0;
  word_datalength = 0;
  while (word_start + word_datalength < input_length) {
    int ch = input[word_start + word_datalength];
    if (!in_tag && isspace(ch)) break;
    if (ch == '<') {
      in_tag = true;
    } else if (ch == '>') {
      if (!in_tag) {
        printf("Improper HTML tags!!!\n");
        exit(1);
      }
      in_tag = false;
    } else if (!in_tag && (ch < 128 || ch >= 192)) {
      word_fmtlength += 1;
    }
    word_datalength += 1;
  }
}

int main(void) {
  // Read the input
  int res;
  while ((res = read(0, input + input_length, MAX_BUFFER - input_length)) > 0) {
    input_length += res;
  }
  HANDLE_CASE(res < 0);

  // Count the number of lines
  for (int i = 0; i < input_length; ++i) {
    if (input[i] == '\n') lines += 1;
  }

  // Indent the first line based on the input's first line
  for (int i = 0; i < input_length && (input[i] == ' ' || input[i] == '\t');
       ++i) {
    put(input[i]);
    indent_str[i] = input[i];
    indent_length += 1;
    if (input[i] == '\t') indent_length += 7;
  }
  cur_width = indent_length;

  // If there are multiple lines indent the rest of them based on the second
  // line's indent
  if (lines > 1) {
    uint8_t *newline = memchr(input, '\n', input_length);
    assert(newline != NULL);
    int start = newline - input + 1;
    int i = start;
    indent_length = 0;
    while (i < input_length && (input[i] == ' ' || input[i] == '\t')) {
      indent_str[i - start] = input[i];
      indent_length += 1;
      if (input[i] == '\t') indent_length += 7;
      i += 1;
    }
    indent_str[i - start] = 0;
  }

  // Indent
  bool need_space = false;
  find_next_word();
  while (word_start < input_length) {
    if (need_space && 1 + cur_width + word_fmtlength > WIDTH) {
      need_space = false;
      add_newline();
      continue;
    } else {
      if (need_space) {
        put(' ');
        cur_width += 1;
      }
      need_space = true;
      for (int i = 0; i < word_datalength; ++i) put(input[word_start + i]);
      cur_width += word_fmtlength;
      word_start += word_datalength;
      find_next_word();
    }
  }
  put('\n');

  // Print the output
  HANDLE_CASE(write(1, output, output_length) != output_length);

  return 0;
}
