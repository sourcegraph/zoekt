

OBJECTIVE
=========

Provide full text code search for git based corpuses.

Goals:

* sub-50ms results on large codebases, such as Android (~2G text) or
  Chrome

* works well on a single standard Linux machine, with stable storage on SSD

* search multiple repositories and multiple branches.

* provide rich query language, with boolean operators

* integrate with Gerrit/Gitiles code review/browsing system


SEARCHING AND INDEXING
======================


Positional trigrams
-------------------

We build an index of ngrams (n=3), where we store the offset of each
ngram's occurrence within a file.  For example, if the corpus is "banana"
then we generate the index

    "ban": 0
    "ana": 1,3
    "nan": 2

If we are searching for a string (eg. "The quick brown fox"), then we
look for two trigrams (eg. "The" and "fox"), and check that they are
found at the right distance apart.

Regular expressions are handled by extracting normal strings from the regular
expressions. For example, to search for

    (Path|PathFragment).*=.*/usr/local

we look for

    (AND (OR substr:"Path" substr:"PathFragment") substr:"/usr/local")

and any documents thus found would be searched for the regular
expression.

Compared to indexing 3-grams on a per-file basis, as described
[here](https://swtch.com/~rsc/regexp/regexp4.html), there are some advantages:

* for each substring, we only have to intersect just a couple of posting-lists:
  one for the beginning, and one for the end.

* Since we touch few posting lists per query, they can be stored on
  slower media, such as SSD.

* we can select any pair of trigrams from the pattern for which the
  number of matches is minimal. For example, we could search for "qui"
  rather than "the".

There are some downsides compared to trigrams:

* The index is large. Empirically, it is about 3x the corpus size, composed of
  2x (offsets), and 1x (original content). However, since we have to look at
  just a limited number of ngrams, we don't have to keep the index in memory.

Compared to [suffix
arrays](https://blog.nelhage.com/2015/02/regular-expression-search-with-suffix-arrays/),
there are the following advantages:

* The index construction is straightforward, and can easily be made
  incremental.

* Since the posting lists for a trigram can be stored on SSD,
  searching with positional trigrams only requires 1.2x corpus size of
  RAM.

* All the matches are returned in document order. This makes it
  straightforward to process compound boolean queries with AND and OR.

Downsides compared to suffix array:

* there is no way to transform regular expressions into index ranges into
  the suffix array.


Case sensitivity
----------------

Code usually is searched without regard for case. In this case, when
we are looking for "abc", we look for occurrences of all the different
case variants, ie. {"abc", "Abc", "aBc", "ABc", ... }, and then
compare the candidate matches without regard for case.


UTF-8
-----

UTF-8 is the defacto encoding for unicode. Zoekt assumes that files
are UTF-8 encoded.  Characters have differing widths in UTF-8, so we
use rune offsets in the trigram index, and convert those back to bytes
with a lookup table: every 100 runes, we store the rune-index to
byte-index mapping. For corpuses that are completely ASCII (fairly
normal for source code), we short-circuit this lookup.


Branches
--------

Each file blob in the index has a bitmask, representing the branches
in which the content is found, eg:

    branches: [master=1, staging=2, stable=4]
    file "x.java", branch mask=3
    file "x.java", branch mask=4

in this case, the index holds two versions of "x.java", the one
present in "master" and "staging", and the one in the "stable" branch.

With this technique, we can index many similar branches of a
repository with little space overhead.


Index format
------------

The index is organized in shards, where each shard is a file, laid out
such that it can be mmap'd efficiently.

Each shard contains data for one code repository. The basic data in an
index shard are the following

   * file contents
   * filenames
   * the content posting lists (varint encoded)
   * the filename posting lists (varint encoded)
   * branch masks
   * metadata (repository name, index format version, etc.)

In practice, the shard size is about 3.5x the corpus size, composed of
original content, posting lists, and other metadata.

The format uses uint32 for all offsets, so the total size of a shard
should be below 4G. Given the size of the posting data, this caps
content size per shard at 1G.

Currently, within a shard, a single goroutine searches all documents,
so the shard size determines the amount of parallelism, and large
repositories should be split across multiple shards to achieve good
performance.

The metadata section contains a version number (which by convention is
also part of the file name of the shard). This provides a smooth
upgrade path across format versions: generate shards in the new
format, kill old search service, start new search service, delete old
shards.


Ranking
-------

In absense of advanced signals (e.g. pagerank on symbol references),
ranking options are limited: the following signals could be used for
ranking

   * number of atoms matched
   * closeness to matches for other atoms
   * quality of match: does match boundary coincide with a word boundary?
   * file latest update time
   * filename lengh
   * tokenizer ranking: does a match fall comment or string literal?
   * symbol ranking: it the match a symbol definition?

For the latter, it is necessary to find symbol definitions and other
sections within files on indexing. Several (imperfect) programs to do
this already exist, eg. `ctags`.

Zoekt also supports an alternative BM25-based scoring algorithm that can be
enabled with `UseBM25Scoring`. When enabled, each match in a file is treated
as a term, and an approximation to BM25 is computed. This is useful for
multi-term queries, better handling of term frequency, and appropriate
document length normalization.


Query language
--------------

Queries are stored as expression trees, using the following data
structure:

    Query:
        Atom
        | AND QueryList
        | OR QueryList
        | NOT Query
        ;

    Atom:
        ConstQuery
        | SubStringQuery
        | RegexpQuery
        | RepoQuery
        | BranchQuery
        ;

Both SubStringQuery and RegexpQuery can apply to either file or
contents, and can optionally be case-insensitive.

ConstQuery (match everything, or match nothing) is a useful construct
for partial evaluation of a query: for each index shard through which
we search, we partially evaluate the query, eg. when the query is

    and[substr:"needle" repo:"zoekt"]

then we can rewrite the query to FALSE if we are looking at a shard
for repository "bazel", skipping the entire shard.

Each query must have at least one positive atom. Negations can only
serve to prune results generated by positive atoms.


Query parsing
-------------

Strings in the input language are considered regular expressions
but literal regular expressions are simplified to Substring queries,

    a.*b => regexp:"a.*b"
    a\.b => substring:"a.b"

leading modifiers select different types of atoms, eg.

    file:java => Substring_file:"java"
    branch:master => Repo:"master"

parentheses inside a string (possibly with escaped spaces) are
interpreted as regular expressions, otherwise they are used for grouping

    (abc def) => and[substring:"abc" substring:"def"]
    (abc\ def) => regexp:"(abc def)"

there is an implicit "AND" between elements of a parenthesized list.
There is an "OR" operator, which has lower priority than the implicit
"AND":

    ppp qqq or rrr sss => or[and[substring:"ppp" substring:"qqq"] and[substring:"rrr" substring:"sss"]]


GERRIT/GITILES INTEGRATION
==========================

Gerrit is a popular system for code review on open source
projects. Its sister project Gitiles provides a browsing experience.

Any code search integration with Gerrit should be made available in
Gitiles.  Gerrit/Gitiles has a complex ACL system, so a codesearch
solution for Gerrit/Gitiles should respect these ACLs.

Since Gitiles knows the identity of the logged-in user, it can
construct search queries that respect ACLs, and even filter results
afterwards if necessary. In such a setup, only Gitiles is allowed to
talk to the search service, so it should be protected from general
access, e.g. by requiring authentication.

A codesearch implementation for Gitiles would change Gitiles to show a
search box on pages relating to a repository. When searching, Gitiles
would also render the search results. The process is as follows:

   * On receiving a query, Gitiles finds the list of branches visible to the user
   * Gitiles sends the raw query, along with branches and repository to the search service
   * The search service parses the query, and embeds it as follows

    (AND original-query repo:REPO (OR "branch:visible-1" "branch:visible-2" .. ))

   * The search service returns the search results, leaving it to
     gitiles to render them. Gitiles can apply any further filtering
     as necessary.


SERVICE MANAGEMENT
==================

The above details how indexing and searching works. A fully fledged
system also crawls repositories and (re)indexes them. Since the system
is designed to run on a single machine, we provide a service
management tool, with the following responsibilities:

   * Poll git hosting sites (eg. github.com, googlesource.com), to fetch new updates
   * Reindex any changed repositories
   * Run the webserver; and restart if it goes down for any reason
   * Delete old webserver logs


Security
--------

This section assumes that 'zoekt' is used as a public facing
webserver, indexing publicly available data, serving on HTTPS without
authentication.

Since the UI is unauthenticated, there are no authentication secrets to steal.

Since the code is public, there is no sensitive code to steal.

This leaves us with the following senstive data:

   * Credentials for accesssing git repostitories (eg. github access token)
   * TLS server certificates
   * Query logs

The system handles the following untrusted data:

   * code in git repositories
   * search queries

Since 'zoekt' itself is written in Go, it does not have memory
security problems: at worst, a bug in the query parser would lead to a
crash.

The code to index is handled by `ctags` for symbol detection. The
security risk this poses is mitigated by using a seccomp based
sandboxing.


Privacy
-------

Webserver logs can contain privacy sensitive data (such as IP
addresses and search queries). For this reason, the service management
tool deletes them after a configurable period of time.
