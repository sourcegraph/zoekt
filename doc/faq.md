# Frequently asked questions

## Why codesearch?

Software engineering is more about reading than writing code, and part
of this process is finding the code that you should read. If you are
working on a large project, then finding source code through
navigation quickly becomes inefficient.

Search engines let you find interesting code much faster than browsing
code, in much the same way that search engines speed up finding things
on the internet.

## Can you give an example?

I had to implement SSH hashed hostkey checking on a whim recently, and
here is how I quickly zoomed into the relevant code using
[our public zoekt instance](http://cs.bazel.build):

* [hash host ssh](http://cs.bazel.build/search?q=hash+host+ssh&num=50): more than 20k results in 750 files, in 3 seconds

* [hash host r:openssh](http://cs.bazel.build/search?q=hash+host+r%3Aopenssh&num=50): 6k results in 114 files, in 20ms

* [hash host r:openssh known_host](http://cs.bazel.build/search?q=hash+host+r%3Aopenssh+known_host&num=50): 4k result in 42 files, in 13ms

the last query still yielded a substantial number of results, but the
function `hash_host` that I was looking for was the 3rd result from
the first file.

## What features make a code search engine great?

Often, you don't know exactly what you are looking for, until you
found it. Code search is effective because you can formulate an
approximate query, and then refine it based on results you got. For
this to work, you need the following features:

* Coverage: the code that interests you should be available for searching

* Speed: search should return useful results quickly (sub-second), so
  you can iterate on queries

* Approximate queries: matching should be done case insensitively, on
  arbitrary substrings, so we don't have to know what we are looking
  for in advance.

* Filtering: we can winnow down results by composing more specific queries

* Ranking: interesting results (eg. function definitions, whole word
  matches) should be at the top.

## How does `zoekt` provide for these?

* Coverage: `zoekt` comes with tools to mirror parts of common Git
  hosting sites. `cs.bazel.build` uses this to index most of the
  Google authored open source software on github.com and
  googlesource.com.

* Speed: `zoekt` uses an index based on positional trigrams. For rare
  strings, eg. `nienhuys`, this typically yields results in ~10ms if
  the operating system caches are warm.

* Approximate queries: `zoekt` supports substring patterns and regular
  expressions, and can do case-insensitive matching on UTF-8 text.

* Filtering: you can filter query by adding extra atoms (eg. `f:\.go$`
  limits to Go source code), and filter out terms with `-`, so
  `\blinus\b -torvalds` finds the Linuses other than Linus Torvalds.

* Ranking: zoekt uses
  [ctags](https://github.com/universal-ctags/ctags) to find
  declarations, and these are boosted in the search ranking.


## How does this compare to `grep -r`?

Grep lets you find arbitrary substrings, but it doesn't scale to large
corpuses, and lacks filtering and ranking.

## What about my IDE?

If your project fits into your IDE, than that is great.
Unfortunately, loading projects into IDEs is slow, cumbersome, and not
supported by all projects.

## What about the search on `github.com`?

GitHub's search has great coverage, but unfortunately, its search
functionality doesn't support arbitrary substrings. For example, a
query [for part of my
surname](https://github.com/search?utf8=%E2%9C%93&q=nienhuy&type=Code)
does not turn up anything (except this document), while
[my complete
name](https://github.com/search?utf8=%E2%9C%93&q=nienhuys&type=Code)
does.

## What about Etsy/Hound?

[Etsy/hound](https://github.com/etsy/hound) is a code search engine
which supports regular expressions over large corpuses, it is about
10x slower than zoekt. However, there is only rudimentary support for
filtering, and there is no symbol ranking.

## What about livegrep?

[livegrep](https://livegrep.com) is a code search engine which
supports regular expressions over large corpuses. However, due to its
indexing technique, it requires a lot of RAM and CPU.  There is only
rudimentary support for filtering, and there is no symbol ranking.

## How much resources does `zoekt` require?

The search server should have local SSD to store the index file (which
is 3.5x the corpus size), and have at least 20% more RAM than the
corpus size. For optimal performance with large codebases, consider
using machines with ample CPU cores, as search operations can be
parallelized across shards.

## Can I index multiple branches?

Yes. You can index 64 branches (see also
https://github.com/google/zoekt/issues/32). Files that are identical
across branches take up space just once in the index.

## How fast is the search?

Rare strings, are extremely fast to retrieve, for example `r:torvalds
crazy` (search "crazy" in the linux kernel) typically takes [about
7-10ms on
cs.bazel.build](http://cs.bazel.build/search?q=r%3Atorvalds+crazy&num=70).

The speed for common strings is dominated by how many results you want
to see. For example [r:torvalds license] can give some results
quickly, but producing [all 86k
results](http://cs.bazel.build/search?q=r%3Atorvalds+license&num=50000)
takes between 100ms and 1 second. Then, streaming the results to your
browser, and rendering the HTML takes several seconds.

## How fast is the indexer?

The Linux kernel (55K files, 545M data) takes about 160s to index on
my x250 laptop using a single thread.  The process can be parallelized
for speedup.

## What does [cs.bazel.build](https://cs.bazel.build/) run on?

Currently, it runs on a single Google Cloud VM with 16 vCPUs, 60G RAM and an
attached physical SSD.

## How does `zoekt` work?

In short, it splits up the file in trigrams (groups of 3 unicode
characters), and stores the offset of each occurrence. Substrings are
found by searching different trigrams from the query at the correct
distance apart.

## I want to know more

Some further background documentation

 * [Designdoc](design.md) for technical details
 * [Godoc](https://godoc.org/github.com/google/zoekt)
 * Gerrit 2016 user summit: [slides](https://storage.googleapis.com/gerrit-talks/summit/2016/zoekt.pdf)
 * Gerrit 2017 user summit: [transcript](https://gitenterprise.me/2017/11/01/gerrit-user-summit-zoekt-code-search-engine/),  [slides](https://storage.googleapis.com/gerrit-talks/summit/2017/Zoekt%20-%20improved%20codesearch.pdf), [video](https://www.youtube.com/watch?v=_-KTAvgJYdI)
