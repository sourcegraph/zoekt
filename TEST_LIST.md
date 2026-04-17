# Multi-Branch Delta Test List

Goal: define failing tests that should pass once delta indexing can update multi-branch indexes across branch-set changes. These tests should verify that the delta path is actually used and that branch-filtered lookups return the same results as a clean full rebuild.

Branch-set-changing deltas are explicitly opt-in via `gitindex.Options.AllowDeltaBranchSetChange`. With that option enabled, unmatched old/new branch slots are allowed to use the conservative full-live delta path; the option itself is the caller's provenance that branch-set changes are intentional. Without that option, branch-set mismatches should preserve the old behavior and fall back to a normal rebuild.

## Test Harness Expectations

- Each test should build an initial full index, mutate the Git repo, request a delta build, and assert the delta build path was used.
- Each test should compare search results against a clean full rebuild of the final branch set.
- Search assertions should include unfiltered search and branch-filtered search for every old, new, unchanged, added, and removed branch involved.
- Search assertions should verify both positive hits and absence of stale hits.
- Tests should inspect repository metadata branch names and versions after the delta.
- Tests should inspect that old shards carry the expected file tombstones when paths changed or disappeared.
- Tests should cover both default branch filters and `query.BranchesRepos` because Sourcegraph branch filtering uses `BranchesRepos`.
- Where `ResolveHEADToBranch` is involved, tests should verify stored short branch names, not `HEAD`, unless the worktree is detached.

## Baseline Existing-Behavior Controls

1. Exact same multi-branch set, one branch content changes.
   - Initial: `[main, release]`
   - Final: `[main, release]`
   - Change: `main: shared.txt` changes, `release: shared.txt` unchanged.
   - Expect: delta used; `branch:main` sees new main content; `branch:release` sees old release content; old main content is absent.

2. Exact same multi-branch set, one branch deletes a path that still exists on another branch.
   - Initial: `[main, release]`
   - Final: `[main, release]`
   - Change: `main` deletes `shared.txt`, `release` keeps it.
   - Expect: delta used; `branch:main` misses `shared.txt`; `branch:release` still finds `shared.txt`.

3. Exact same multi-branch set, a file becomes identical across two branches.
   - Initial: `main: a.txt = A`, `release: a.txt = B`
   - Final: `main: a.txt = B`, `release: a.txt = B`
   - Expect: delta used; one logical final document may serve both branches, but branch-filtered searches for both branches must find it.

4. Exact same multi-branch set, a file diverges from shared content.
   - Initial: `main` and `release` both have `a.txt = same`.
   - Final: `main: a.txt = new-main`, `release: a.txt = same`.
   - Expect: delta used; branch masks are split correctly.

## Single Branch Rename

5. Rename one branch in a two-branch index, no file changes on renamed branch.
   - Initial: `[feature-a, release]`
   - Final: `[feature-b, release]`
   - `feature-b` points to the same commit as old `feature-a`.
   - Expect: delta used; metadata says `[feature-b, release]`; `branch:feature-b` finds old feature content; `branch:feature-a` finds nothing.

6. Rename one branch and modify a path on renamed branch.
   - Initial: `[feature-a, release]`
   - Final: `[feature-b, release]`
   - Change: `feature-b: branch.txt` differs from old `feature-a`.
   - Expect: delta used; new feature content found only on `feature-b`; old feature content absent; release content preserved.

7. Rename one branch and delete a path on renamed branch.
   - Initial: `[feature-a, release]`
   - Final: `[feature-b, release]`
   - Change: `feature-b` deletes `branch.txt`; `release` keeps unrelated content.
   - Expect: delta used; `branch:feature-b` misses deleted path; stale `feature-a` path absent; release queries unchanged.

8. Rename one branch and add a path on renamed branch.
   - Initial: `[feature-a, release]`
   - Final: `[feature-b, release]`
   - Change: `feature-b` adds `new.txt`.
   - Expect: delta used; `branch:feature-b` finds `new.txt`; `branch:feature-a` finds nothing.

## Multiple Branch Renames

9. Rename two branches at the same time with no file changes.
   - Initial: `[feature-a, qa-a, release]`
   - Final: `[feature-b, qa-b, release]`
   - Expect: delta used; both renamed branch filters work; old branch filters return no results; release unchanged.

10. Rename two branches with independent file changes.
    - Initial: `[feature-a, qa-a, release]`
    - Final: `[feature-b, qa-b, release]`
    - Change: `feature-b` modifies `feature.txt`; `qa-b` modifies `qa.txt`.
    - Expect: delta used; each branch sees only its new content; old content absent.

11. Rename two branches where both end up sharing the same blob for a path.
    - Initial: `feature-a: shared.txt = A`, `qa-a: shared.txt = B`
    - Final: `feature-b: shared.txt = C`, `qa-b: shared.txt = C`
    - Expect: delta used; final branch masks include both renamed branches.

12. Rename branch order changes at the same time.
    - Initial: `[feature-a, release, qa-a]`
    - Final: `[qa-b, feature-b, release]`
    - Expect: delta used when `AllowDeltaBranchSetChange` is set; branch-filtered search must match clean rebuild exactly.

## Add Branches

13. Add one new branch that is identical to an existing branch.
    - Initial: `[main]`
    - Final: `[main, release]`
    - `release` points to same commit as `main`.
    - Expect: delta used; existing docs gain `release` membership; `branch:release` finds the same files as `branch:main`.

14. Add one new branch with one changed path.
    - Initial: `[main]`
    - Final: `[main, release]`
    - `release` branches from `main` and modifies `release.txt`.
    - Expect: delta used; `branch:release` sees release-only file/content; `branch:main` does not.

15. Add one new branch with deletions relative to existing branch.
    - Initial: `[main]`
    - Final: `[main, slim]`
    - `slim` deletes `large.txt`.
    - Expect: delta used; `branch:slim` misses `large.txt`; `branch:main` still finds it.

16. Add multiple new branches at once.
    - Initial: `[main]`
    - Final: `[main, release, dev]`
    - `release` and `dev` each have distinct path changes.
    - Expect: delta used; all three branch filters match clean rebuild.

17. Add a new branch that shares some docs and diverges on others.
    - Initial: `[main]`
    - Final: `[main, feature]`
    - `feature` changes `a.txt`, keeps `b.txt`, adds `c.txt`, deletes `d.txt`.
    - Expect: delta used; branch masks correct for all four categories.

## Remove Branches

18. Remove one branch, no file content changes.
    - Initial: `[main, release]`
    - Final: `[main]`
    - Expect: delta used; `branch:release` returns no results; unfiltered search does not duplicate stale release-only docs.

19. Remove one branch that had branch-only files.
    - Initial: `[main, release]`
    - Final: `[main]`
    - `release` had `release-only.txt`.
    - Expect: delta used; release-only file absent from unfiltered and branch-filtered searches.

20. Remove one branch while another remaining branch still has the same path with different content.
    - Initial: `main: shared.txt = main`, `release: shared.txt = release`
    - Final: `[main]`
    - Expect: delta used; final unfiltered and `branch:main` searches show only main content.

21. Remove multiple branches at once.
    - Initial: `[main, release, dev, qa]`
    - Final: `[main]`
    - Expect: delta used; removed branch filters return no results; main results match clean rebuild.

22. Remove a branch and modify a retained branch in the same update.
    - Initial: `[main, release]`
    - Final: `[main]`
    - Change: `main` modifies `a.txt`; `release` removed.
    - Expect: delta used; new main content present; old release-only content absent.

## Combined Rename/Add/Remove

23. Rename one branch and add one branch.
    - Initial: `[feature-a, release]`
    - Final: `[feature-b, release, dev]`
    - Expect: delta used; renamed branch, unchanged branch, and added branch all match clean rebuild.

24. Rename one branch and remove one branch.
    - Initial: `[feature-a, release, dev]`
    - Final: `[feature-b, release]`
    - Expect: delta used; `feature-b` correct, `dev` absent, release unchanged.

25. Add one branch and remove one branch.
    - Initial: `[main, old-release]`
    - Final: `[main, new-release]`
    - Expect: delta used; old-release absent; new-release present.

26. Rename multiple branches, add one branch, remove one branch.
    - Initial: `[feature-a, qa-a, old-release, main]`
    - Final: `[feature-b, qa-b, new-release, main]`
    - Expect: delta used; branch-filtered searches for every final branch match clean rebuild; every removed branch returns no results.

27. Branch count changes and branch order changes at the same time.
    - Initial: `[main, release, dev]`
    - Final: `[qa, main, release-renamed]`
    - Expect: delta used only with explicit old-to-new mapping; results must be order-independent.

## HEAD Resolution And Worktrees

28. Single requested `HEAD` resolves from `feature-a` to `feature-b`.
    - Initial indexed branch: `[feature-a]`
    - Final indexed branch: `[feature-b]`
    - Expect: delta used; metadata stores `feature-b`; old branch filter returns no results.

29. Multi-branch `HEAD, release`, where `HEAD` resolves from `feature-a` to `feature-b`.
    - Initial: `[feature-a, release]`
    - Final: `[feature-b, release]`
    - Expect: delta used; same assertions as single branch rename plus release preservation.

30. Multi-branch `HEAD, release`, where `HEAD` resolves to `release`.
    - Initial or final branch list would contain duplicate logical branches.
    - Expect: either deterministic dedupe with correct branch masks or conservative full rebuild; define chosen behavior before implementation.

31. Detached `HEAD` in a multi-branch index.
    - Initial: `[feature-a, release]`
    - Final: `[HEAD, release]`
    - Expect: define whether detached HEAD can delta; if allowed, branch-filtered lookup for `HEAD` must match clean rebuild.

32. Two linked worktrees of the same repo in the same index dir.
    - Worktree A: `HEAD -> feature-a`
    - Worktree B: `HEAD -> feature-b`
    - Expect: indexes either have distinct repo identities or explicit behavior; branch metadata must not conflate both as `HEAD`.

## Ambiguity And Safety Tests

33. Unmatched old/new branch names with branch-set delta opt-in.
    - Initial: `[foo, bar]`
    - Final: `[baz, bar]`
    - With `AllowDeltaBranchSetChange`, expect delta used; final results match clean rebuild and branch mapping is logged.
    - Without `AllowDeltaBranchSetChange`, expect old behavior: full rebuild fallback.

34. Many-to-one branch update with branch-set delta opt-in.
    - Initial: `[a, b]`
    - Final: `[c]`
    - With `AllowDeltaBranchSetChange`, expect delta used via the conservative full-live path; final results match clean rebuild.
    - Without the flag, expect fallback.

35. One-to-many branch split with branch-set delta opt-in.
    - Initial: `[a]`
    - Final: `[b, c]`
    - With `AllowDeltaBranchSetChange`, expect delta used via the conservative full-live path; final results match clean rebuild.
    - Without the flag, expect fallback.

36. Duplicate final branch names after wildcard expansion.
    - Initial: `[main]`
    - Final request expands to `[main, main]`
    - Expect deterministic dedupe when branch-set changes are allowed, or full rebuild otherwise; never duplicate branch masks.

37. Branch removed and re-added with the same name but unrelated history.
    - Initial: `[release]` at old lineage
    - Final: `[release]` at unrelated commit
    - Expect: delta can still be correct if old and new commits are diffable or fallback if commit ancestry/object availability is insufficient.

38. Old commit missing locally for a removed/renamed branch.
    - Expect: full rebuild; no partial stale results.

39. New branch commit missing locally after fetch/filter.
    - Expect: full rebuild or hard error according to existing fetch semantics; no partial index.

40. Compound shards present before multi-branch branch-set change.
    - Expect: current unsupported compound-shard delta behavior remains conservative until separately implemented.

## Stats And Observability

41. DeltaStats after branch rename.
    - Expect: live document/path counts match clean rebuild; physical counts include stacked debt; tombstones reflect affected paths.

42. DeltaStats after adding branch identical to existing branch.
    - Expect: live document count may not grow for shared docs, but branch membership changes must be reflected; counters match chosen semantics.

43. DeltaStats after removing branch with branch-only files.
    - Expect: live counts decrease; physical counts retain old docs until rebuild; tombstone/path debt increases appropriately.

44. Admission log for successful multi-branch branch-set delta.
    - Expect: JSONL contains mapping summary, accepted=true, old/new branch counts, and cost metrics.

45. Admission log for fallback due ambiguous mapping.
    - Expect `accepted=true` for unmatched branch mappings when `AllowDeltaBranchSetChange` is set and the conservative delta path is safe.
    - Keep `accepted=false` fallback logging for structurally unsafe cases such as missing commits or unsupported compound shards.

## Query Surfaces To Exercise

46. Plain substring query across all branches.
47. `branch:` parsed query.
48. `query.BranchesRepos` query for one branch and repo ID.
49. `BranchesRepos` query with multiple branch/repo pairs.
50. File-name query after branch rename/add/remove.
51. Regex content query after branch rename/add/remove.
52. Case-sensitive content query after branch rename/add/remove.

## Comparison Strategy

For each successful delta test:

1. Build initial index.
2. Mutate branch set and branch contents.
3. Run delta indexing with spies and require the delta path, not fallback.
4. Build a clean full index in a separate temp dir for the final branch set.
5. Compare search results for all relevant queries between delta and clean indexes.
6. Compare repository branch metadata.
7. Compare advisory live `DeltaStats` fields against the clean full index when `stats-v1` is enabled.
