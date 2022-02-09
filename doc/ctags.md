
# CTAGS

Ctags generates indices of symbol definitions in source files. It
started its life as part of the BSD Unix, but there are several more
modern flavors. Zoekt supports both [exuberant
ctags](http://ctags.sourceforge.net/) and
[universal-ctags](https://github.com/universal-ctags).

It is strongly recommended to use Universal Ctags, [version
`db3d9a6`](https://github.com/universal-ctags/ctags/commit/4ff09da9b0a36a9e75c92f4be05d476b35b672cd)
or newer, running on the Linux platform.

From this version on, universal ctags will be called using seccomp,
which guarantees that security problems in ctags cannot escalate to
access to the indexing machine.

Ubuntu, Debian and Arch provide universal ctags with seccomp support
compiled in. Zoekt expects the `universal-ctags` binary to be on
`$PATH`. Note: only Ubuntu names the binary `universal-ctags`, while
most distributions name it `ctags`.

## Setup

### Option 1: Install through package manager

It is possible to install `ctags` on Ubuntu via `apt`:

```
sudo apt install universal-ctags
```

### Option 2: Compile from source

Use the following invocation to compile and install universal-ctags:

```sh
sudo apt-get install
  pkg-config autoconf \
  libseccomp-dev libseccomp \
  libjansson-dev libjansson 

./autogen.sh
LDFLAGS=-static ./configure --enable-json --enable-seccomp
make -j4

# create tarball
NAME=ctags-$(date --iso-8601=minutes | tr -d ':' | sed 's|\+.*$||')-$(git show --pretty=format:%h -q)
mkdir ${NAME}
cp ctags ${NAME}/universal-ctags
tar zcf ${NAME}.tar.gz ${NAME}/
```

## Indexing with ctags

Zoekt runs `ctags` with a hard coded list of languages by default.
However, it's possible to pass only the languages available on your system
by wrapping `ctags` in a script.

1. Create a file `custom-ctags.sh`:

```sh
#!/usr/bin/bash

set -o pipefail
set -eux

CTAGS_LANGUAGES=$(/usr/bin/universal-ctags --list-languages | grep -v 'disabled' | tr '\n' ',' | sed 's/.$//')
/usr/bin/universal-ctags --languages=$CTAGS_LANGUAGES $@
```

2. Make it executable:

```sh
chmod +x custom-ctags.sh
```

3. Set the environment variable when indexing with Zoekt:

```sh
export CTAGS_COMMAND=$PWD/custom-ctags.sh
go run cmd/zoekt-index/main.go -index /tmp/zoekt-index /path/to/repository
```