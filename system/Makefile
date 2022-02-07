.PHONY: clean all

scripts_src := $(wildcard *.sh)
scripts_dst := $(patsubst %,$(HOME)/.sbin/%,$(scripts_src))

sources := $(wildcard *.c)
binaries := $(patsubst %.c,$(HOME)/.sbin/%,$(sources))

all: $(scripts_dst) $(binaries)

common_opts := -std=c99 -g -Wall -Wextra -Werror -O2
link_opts := -lm -static

clean:
	rm $(binaries)

$(HOME)/.sbin/halt: halt.c
	@echo building $@
	@gcc $(common_opts) -o $@ $< $(link_opts)
	@echo adding setuid rights
	@chmod +s $(HOME)/.sbin/halt
	@echo linking reboot and poweroff
	@ln -s halt $(HOME)/.sbin/reboot &>/dev/null || true
	@ln -s halt $(HOME)/.sbin/poweroff &>/dev/null || true

$(HOME)/.sbin/%: %.c
	@echo building $@
	@gcc $(common_opts) -o $@ $< $(link_opts)

$(HOME)/.sbin/%: %
	@echo copying $@
	@cp $(PWD)/$< $@
