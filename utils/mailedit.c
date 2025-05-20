// mailedit - tool to sanity check my email messages.
//
// mailedit is a wrapper around vim for editing email messages from mutt. upon
// vim's exit it checks the resulting message's to and cc email headers to see
// if they contain valid email addresses. the goal is to prevent myself from
// having typos in those fields. i consider an address "valid" if my whitelist
// contains it. the whitelist, ~/.emails, is just a newline separated list of
// email addresses that i allow myself to send emails to. if mailedit finds an
// invalid email message, it presents three options to the user:
//
// - ignore the error, allow mutt to use the message as is,
// - add the missing contacts to ~/.emails,
// - go back into vim to edit the message (e.g. to fix a typo).
//
// the last option restarts mailedit from the start; the other two break out of
// the main loop.

#include <ctype.h>
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// check is like an assert but always enabled.
#define check(cond) checkfunc(cond, #cond, __FILE__, __LINE__)
void checkfunc(bool ok, const char *s, const char *file, int line) {
  if (ok) return;
  printf("checkfail at %s %d %s\n", file, line, s);
  if (errno != 0) printf("errno: %m\n");
  exit(1);
}

enum { contactslimit = 10000 };
enum { contactlenlimit = 70 };

struct contact {
  char name[contactlenlimit + 1];
};

// both contacts and recipients are sorted.
static int contacts;
static struct contact contact[contactslimit];
static int recipients;
static struct contact recipient[contactslimit];

int contactcmp(const void *a, const void *b) { return strcmp(a, b); }

void sortuniq(struct contact *contact, int *contacts) {
  if (*contacts == 0) return;
  qsort(contact, *contacts, sizeof(contact[0]), contactcmp);
  int newend = 1;
  for (int i = 1; i < *contacts; i++) {
    if (strcmp(contact[i - 1].name, contact[i].name) != 0) {
      if (newend != i) {
        contact[newend] = contact[i];
      }
      newend++;
    }
  }
  *contacts = newend;
}

int main(int argc, char **argv) {
  if (argc != 2) {
    puts("mailedit - tool to sanity check my email messages, see mailedit.c.");
    puts("usage: mailedit [fname]");
    exit(1);
  }
  char contactfile[120];
  snprintf(contactfile, 110, "%s/.emails", getenv("HOME"));
  char cmd[1000];
  snprintf(cmd, 990,
           "grep -qz '^<' %s; html=$?;"
           "test 0 = $html && bm -r <%s | sponge %s;"
           "vim -X -c :Mailmode %s;"
           "test 0 = $html && bm <%s | sponge %s;"
           "true",
           argv[1], argv[1], argv[1], argv[1], argv[1], argv[1]);
  if (strlen(cmd) > 980) {
    printf("%s too long, aborting.\n", argv[1]);
    exit(1);
  }
  do {
    check(system(cmd) == 0);

    // read the contacts.
    FILE *f = fopen(contactfile, "r");
    check(f != NULL);
    char line[10000];
    contacts = 0;
    while (fscanf(f, "%90s", line) == 1) {
      check(strlen(line) <= contactlenlimit);
      strcpy(contact[contacts].name, line);
      check(contacts++ < contactslimit);
    }
    check(fclose(f) == 0);
    sortuniq(contact, &contacts);

    // parse the to and cc fields from the email message.
    f = fopen(argv[1], "r");
    check(f != NULL);
    bool addressmode = false;
    recipients = 0;
    while (fgets(line, sizeof(line), f) != 0) {
      int len = strlen(line);
      check(len < 9900);
      if (line[0] == 0 || line[0] == '\n') break;
      for (int i = 0; i < len; i++) line[i] = tolower((unsigned char)line[i]);
      char *ptr = line;
      if (strncmp(line, "to:", 3) == 0 || strncmp(line, "cc:", 3) == 0) {
        addressmode = true;
        ptr += 3;
      } else if (line[0] != ' ' && line[0] != '\t') {
        addressmode = false;
      }
      if (!addressmode) continue;
      // addresses are comma separated and optionally put inside <>. for
      // example:
      // To: user1 <user1@example.com>
      // Cc: "user2" <user2@example.com>, user3@example.com,
      //  user4@example.com
      // but does not really matter, just iterate through all tokens and collect
      // the ones that look like email addresses. should cover most of the
      // cases.
      char *tok = strtok(ptr, " \t\n<>,\"");
      while (tok != NULL) {
        if (strchr(tok, '@') != NULL) {
          check(recipients < contactslimit);
          check(strlen(tok) <= contactlenlimit);
          strcpy(recipient[recipients++].name, tok);
        }
        tok = strtok(NULL, " \t\n<>,\"");
      }
    }
    check(fclose(f) == 0);
    qsort(recipient, recipients, sizeof(recipient[0]), contactcmp);
    sortuniq(recipient, &recipients);

    // print the non-whitelisted contacts.
    int missingcnt = 0;
    int a = 0, b = 0;
    while (a < contacts && b < recipients) {
      int cmp = strcmp(contact[a].name, recipient[b].name);
      if (cmp == 0) {
        a++;
        b++;
      } else if (cmp < 0) {
        a++;
      } else {
        missingcnt++;
        puts(recipient[b].name);
        b++;
      }
    }
    while (b < recipients) {
      missingcnt++;
      puts(recipient[b].name);
      b++;
    }

    // show the menu of options to the user.
    if (missingcnt == 0) break;
    printf("%d emails missing from the whitelist. ", missingcnt);
    printf("(i)gnore, (a)dd, (e)dit again?\n");
    char response[16];
    check(scanf("%15s", response) == 1);
    if (response[0] == 'i') break;
    if (response[0] != 'a') continue;

    // add the missing contacts to the .emails file.
    f = fopen(contactfile, "a");
    check(f != NULL);
    a = 0, b = 0;
    while (a < contacts && b < recipients) {
      int cmp = strcmp(contact[a].name, recipient[b].name);
      if (cmp == 0) {
        a++;
        b++;
      } else if (cmp < 0) {
        a++;
      } else {
        fprintf(f, "%s\n", recipient[b].name);
        b++;
      }
    }
    while (b < recipients) {
      fprintf(f, "%s\n", recipient[b].name);
      b++;
    }
    check(fclose(f) == 0);
    break;
  } while (true);
  return 0;
}
