
CTAGS
=====

Ctags generates indices of symbol definitions in source files. It
started its life as part of the BSD Unix, but there are several more
modern flavors. Zoekt supports
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

Use the following invocation to compile and install universal-ctags:

```
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
