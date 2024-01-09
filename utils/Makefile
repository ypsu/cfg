# Build options.

# opt or dbg.
BUILD := opt


# Implementation follows.

$(shell mkdir -p $(HOME)/.bin)
scripts_src := $(filter-out Makefile,$(filter-out %.c %.go,$(sort $(wildcard *))))
scripts_dst := $(patsubst %,$(HOME)/.bin/%,$(scripts_src))
sources := $(sort $(wildcard *.c *.go))
binaries := $(patsubst %.go,$(HOME)/.bin/%,$(patsubst %.c,$(HOME)/.bin/%,$(sources)))
libs := \
	-lasound \
	-lbsd \
	-lcrypto \
	-lm \
	-lncurses \
	-lpcap \
	-lreadline \
	-lrt \
	-lssl \
	-lX11 \
	-lXcursor \
	-lXext \
	-lXss \

flags.opt := -O2
cflags := -g -std=c99 -Wall -Wextra -Werror
compile_cmd := gcc $(cflags) $(flags.$(BUILD))

.PHONY: all clean
all: $(scripts_dst) $(binaries)
clean:
	rm -f $(scripts_dst) $(binaries)
$(HOME)/.bin/%: %.c
	@echo building $(notdir $@)
	@$(compile_cmd) -o $@ $< $(libs)
$(HOME)/.bin/%: %.go
	@echo building $(notdir $@)
	@go build -o $@ $<
$(HOME)/.bin/%: %
	@echo symlinking $(notdir $@)
	@ln -s $(PWD)/$< $@

aptinstall:
	sudo apt install \
		inotify-tools \
		libasound-dev \
		libbsd-dev
