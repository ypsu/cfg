#!/bin/bash
# usync - unionfs-fuse sync. syncs a unionfs rw branch to its ro branch. see
# https://iio.ie/fsbuf.

set -e
shopt -s nullglob
GLOBIGNORE=.:..

# run bunch of safety checks.
if test "$#" -ne 2; then
  echo "Usage: fsbufsync src dst"
  exit 1
fi
src="$(realpath "$1")"
dst="$(realpath "$2")"
if ! [[ "$src" =~ ^[0-9a-z/_]*$ ]]; then
  echo "error: '$src' is too complicated name, not supported."
  exit 1
fi
if ! test -d $src; then
  echo "error: $src is not a directory."
  exit 1
fi
if ! [[ "$dst" =~ ^[0-9a-z/_]*$ ]]; then
  echo "error: '$dst' is too complicated name, not supported."
  exit 1
fi
if ! test -d $dst; then
  echo "error: $dst is not a directory."
  exit 1
fi
if findmnt -T $src | grep -q " ro,"; then
  echo "error: $src is read-only."
  exit 1
fi
if findmnt -T $dst | grep -q " ro,"; then
  echo "error: $dst is read-only."
  exit 1
fi
if find $src -name '.fuse_hidden*' | grep .; then
  echo "error: fuse still has deleted files open:"
  udir="$(mount | awk '/unionfs/{print $3}')"
  find $src -name '.fuse_hidden*' | sed "s:^$src:$udir:" | xargs lsof
  exit 1
fi

# do the actual commit.
if test -d $src/.unionfs; then
  dir=$src/.unionfs
  re="s:^$src/.unionfs/\(.*\)_HIDDEN~:$dst/\1:"
  find $dir -name '*_HIDDEN~' | sed "$re" | xargs -r rm -rfv
  rm -r $dir
fi
if test "$(echo $src/*)" != ""; then
  cp -fprv $src/* $dst
  rm -rf $src/*
fi
echo all ok
