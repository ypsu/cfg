// flashcard utility for remembering trivia.
//
// i got the inspiration for this from the "how to remember anything
// forever-ish" article at https://ncase.me/remember/.
//
// works from ~/.flashcard. that file has the following structure:
//
//   def identifier1 question1~ answer1
//   def identifier2 question2~ answer2
//   def ...
//
//   evt identifier1 date days
//   evt identifier2 date days
//   evt identifier1 date days
//   ...
//
// the def directive defines the available questions and answers. separate the
// question and answer with the '~' character.
//
// the evt ("event") directive records a recall event for a specific trivia. the
// date means the day on the recall happened. the days mean the number of days
// the next recall is due. multiple evt statements can refer to the same trivia
// but it is only the last one that is active; in other words an evt referring
// to the same trivia overrides previous statements to the same trivia.
//
// the point of separating the def and evt directives is to make this tool
// simple. after each recall event all it needs to do is just to append to this
// file. to reset the questions just delete all lines starting with evt. there
// must be exactly one empty line after the def statements.
//
// example:
//
//   def myphone what is my phone number?~ 123-4567
//
//   evt myphone 2019-02-24 1
//   evt myphone 2019-02-25 2
//   evt myphone 2019-02-27 4
//
// this tool is not intended for serious study. but rather for remembering a
// handful of trivia like my partner's and my own telephone numbers. i stick
// this in front of my email client: whenever i run mutt (often), this will run
// first. to avoid being annoying it only asks one question per day, it is a
// noop on subsequent runs on that day.
//
// on startup it reads the contents of ~/.flashcard into memory. if there was an
// recall event for today already, it quits. if there is no recall event that is
// due, it selects the default challenge (see below). then it prints the
// question and asks for the answer. it keeps asking for the answer until the
// user enters the correct answer. there is no way to get it to show the correct
// answer; the user needs to look that up to manually (rationale: making the
// recalling an item painful makes it easier to remember said item). after a
// correct answer it asks the user when should be the next recall event in terms
// of days. the usual flashcard rules apply: the user should enter 1 if answered
// incorrectly or double the previous days if answered correctly (the tool
// displays the current number of days). after that the tool records the session
// in the form of an evt statement and appends it to the file.
//
// the default challenge (identified as "default" in the evt statements but
// without reference to it in the def statements) is a way to get me practice a
// specific skill without much tracking. an indulgence. right now is just a
// multiplication challenge of two relatively small numbers.

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

enum { cardlimit = 1000 };

struct card {
  char *id;
  char *question;
  char *answer;
  time_t triggertime;
  int days;
};

static int cards;
static struct card card[cardlimit];

int cardcmp(const void *a, const void *b) {
  return strcmp(((const struct card *)a)->id, ((const struct card *)b)->id);
}

int main(void) {
  // rough outline:
  // defparsing: parse the def statements.
  // evtparsing: parse the evt statements.
  // presentquestion: pick and present a question.
  // savedata: write the result back into the file.

  // open ~/.flashcard.
  check(chdir(getenv("HOME")) == 0);
  FILE *f = fopen(".flashcard", "r");
  check(f != NULL);
  char line[1000];

  // defparsing: parse the def statements. but first, add the default card.
  card[cards++].id = strdup("default");
  while (fgets(line, 1000, f) != NULL && line[0] != '\n') {
    if (strncmp(line, "def ", 4) != 0) {
      printf("error, line does not start with def: %s", line);
      exit(1);
    }
    check(cards < cardlimit);
    char id[1000], question[1000], answer[1000];
    const char fmt[] = "%900s %900[^~]~ %900[^\n]";
    if (sscanf(line + 4, fmt, id, question, answer) != 3) {
      printf("error, could not parse: %s", line);
      exit(1);
    }
    card[cards].id = strdup(id);
    card[cards].question = strdup(question);
    card[cards].answer = strdup(answer);
    cards++;
  }
  if (feof(f)) {
    puts("error, there must be an empty line after the defs.");
    exit(1);
  }
  qsort(card, cards, sizeof(card[0]), cardcmp);

  // check for duplicate identifiers, just in case.
  for (int i = 1; i < cards; i++) {
    if (strcmp(card[i - 1].id, card[i].id) == 0) {
      printf("error, duplicated id: %s\n", card[i].id);
      exit(1);
    }
  }

  // evtparsing: parse the evt statements.
  time_t todaytime = time(NULL);
  struct tm today = *localtime(&todaytime);
  while (fgets(line, 1000, f) != NULL) {
    if (strncmp(line, "evt ", 4) != 0) {
      printf("error, line does not start with evt: %s", line);
      exit(1);
    }
    char id[1000];
    int year, month, day, days;
    const char fmt[] = "%900s %d-%d-%d %d";
    if (sscanf(line + 4, fmt, id, &year, &month, &day, &days) != 5) {
      printf("error, could not parse line: %s", line);
      exit(1);
    }
    struct card soughtcard = {.id = id};
    struct card *thecard;
    thecard = bsearch(&soughtcard, card, cards, sizeof(card[0]), cardcmp);
    if (thecard == NULL) {
      printf("error, identifier not found: %s\n", id);
      exit(1);
    }
    struct tm tm = {
        .tm_year = year - 1900,
        .tm_mon = month - 1,
        .tm_mday = day,
        .tm_isdst = -1,
    };
    bool wastoday = true;
    wastoday = wastoday && today.tm_year == tm.tm_year;
    wastoday = wastoday && today.tm_mon == tm.tm_mon;
    wastoday = wastoday && today.tm_mday == tm.tm_mday;
    if (wastoday) {
      // this event happened today, do not bother the user anymore.
      return 0;
    }
    tm.tm_mday += days;
    thecard->triggertime = mktime(&tm);
    thecard->days = days;
  }
  check(feof(f));
  check(fclose(f) == 0);

  // presentquestion: pick and present a question.
  struct card *acard = NULL;
  struct card *defaultcard = NULL;
  for (int i = 0; i < cards; i++) {
    // skip the default question for now.
    if (strcmp(card[i].id, "default") == 0) {
      defaultcard = &card[i];
      continue;
    }
    if (card[i].triggertime < todaytime) {
      acard = &card[i];
      break;
    }
  }
  if (acard == NULL) {
    // nothing in due, ask the default one.
    acard = defaultcard;
    srand(time(NULL));
    int a = rand() % 8 + 2;
    int b = rand() % 88 + 12;
    int c;
    printf("%d * %d = ?\n", a, b);
    while (scanf("%d", &c) == 1 && c != a * b) puts("wrong answer, try again!");
    puts("correct!");
  } else {
    puts(acard->question);
    while (fgets(line, 1000, stdin) != NULL) {
      int len = strlen(line);
      if (len > 0 && line[len - 1] == '\n') line[--len] = 0;
      if (strcmp(line, acard->answer) == 0) break;
      puts("wrong answer, try again!");
    }
    printf("correct! repeat was %d days. new value?\n", acard->days);
    check(scanf("%d", &acard->days) == 1);
  }

  // savedata: write the result back into the file.
  f = fopen(".flashcard", "a");
  check(f != NULL);
  char datestr[12];
  check(strftime(datestr, 12, "%F", &today) == 10);
  fprintf(f, "evt %s %s %d\n", acard->id, datestr, acard->days);
  check(fclose(f) == 0);
  return 0;
}
