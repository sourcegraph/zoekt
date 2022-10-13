#!/usr/bin/env bash

# Create a tarball of this system's sysfs filesystem + place it in the home directory.
#
# (This special logic is necessary since /sys is a pseudo-filesystem that exposes kernel variables.
#  The files in /sys and their sizes will frequently change in between read()'s, which can break naive tar invocations.)
#
# Usage: ./snapshot.sh sysfs.tar.gz

dst="$PWD/$1"
tmp=$(mktemp -d -t sysfs_snapshot_XXXXXXX)

cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

set -euxo pipefail

find /sys/devices/*/block /sys/dev/block /sys/class/block -print0 | sort -z | while IFS= read -d $'\0' -r file; do
  # create the new file name by stripping the leading
  # /sys and mashing it against the temp folder
  temp_file="${tmp}/${file#*/sys/}"

  # create equivalent symlink
  if [ -L "$file" ]; then
    cp -d "$file" "$temp_file"
    continue
  fi

  # create necessary directories
  if [ -d "$file" ]; then
    mkdir -p "$temp_file"
    continue
  fi

  # skip over any files that we lack permissions to read,
  # we encounter I/O errors when trying to read, or
  # have some other weirdness
  if ! wc -l "$file" >/dev/null 2>&1; then
    continue
  fi

  cp "$file" "$temp_file"
done

cd "$tmp"
tar vczf "$dst" .
