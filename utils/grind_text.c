#define _GNU_SOURCE
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <termios.h>
#include <unistd.h>

int term_width, term_height;

void get_term_dimensions(void)
{
	struct winsize winsize;
	ioctl(2, TIOCGWINSZ, &winsize);
	term_width = winsize.ws_col;
	term_height = winsize.ws_row;
}

int main(void)
{
	get_term_dimensions();

	int columns = term_width-1;
	char buf[columns+1];
	memset(buf, 0, sizeof buf);
	while (true) {
		for (int i = 0; i < columns; ++i)
			buf[i] = rand()%96 + 32;
		puts(buf);
		usleep(10000);
	}
	return 0;
}
