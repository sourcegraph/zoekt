#!/usr/bin/env python3

'''A manual merge policy to run on Sourcegraph.com while we are
experimenting. This will be ported at some point to be run automatically. For
now it is expected to be run manually.

'''

from collections import defaultdict
from pathlib import Path
import argparse
import subprocess
import time

MAX_REPO_SIZE = 1024 * 1024 * 100
MAX_COMPOUND_SIZE = 2 * 1024 * 1024 * 1024
MIN_AGE_SECONDS = 24 * 60 * 60

def get_shards(indexDir: Path):
    'returns v16 shards grouped by repo. [(mtime, size, paths)]'
    shards = defaultdict(list)

    for child in indexDir.iterdir():
        if not child.is_file() or '_v16' not in child.name:
            continue
        name = child.name
        repo = name[:name.find('_v16')]
        stat = child.stat()
        size = stat.st_size if child.suffix == '.zoekt' else 0
        shards[repo].append((stat.st_mtime, size, child))

    repos = []
    for repo, items in shards.items():
        mtime = min(t for t, _, _ in items)
        size = sum(s for _, s, _ in items)
        paths = [n for _, _, n in items]
        repos.append((mtime, size, paths))

    return repos

def gen_compounds(repos):
    candidates = []
    ignored = []
    min_age = time.time() - MIN_AGE_SECONDS
    for mtime, size, paths in sorted(repos):
        if size <= MAX_REPO_SIZE and mtime <= min_age:
            candidates.append((size, paths))
        else:
            ignored.append((size, paths))

    # Build up a grouping of candidates such that each item in compounds ~ MAX_COMPOUND_SIZE
    compounds = []
    for size, paths in candidates:
        if len(compounds) == 0:
            compounds.append((size, paths[:]))
            continue

        # Look at the latest compound and add to it.
        current_size, current_paths = compounds[-1]

        # We are going over the size, start a new compound.
        if current_size + size > MAX_COMPOUND_SIZE:
            compounds.append((size, paths[:]))
            continue

        # We are within size.
        compounds[-1] = (current_size + size, current_paths + paths)

    for size, paths in compounds:
        print(f'compound shard with {int(size / 1024 / 1024)}MiB and {len(paths)} files')

    total_size = sum(size for size, _ in compounds)
    total_len  = sum(len(paths) for _, paths in compounds)
    print(f'total compound size {int(total_size / 1024 / 1024 / 1024)}GiB and {total_len} files')

    total_size = sum(size for size, _ in ignored)
    total_len  = sum(len(paths) for _, paths in ignored)
    print(f'total non compound size {int(total_size / 1024 / 1024 / 1024)}GiB and {total_len} files')

    return [paths for _, paths in compounds]

def write_status(proc, paths):
    scratch = paths[0].parent / '.scratch'
    scratch.mkdir(parents=True, exist_ok=True)

    now = int(time.time())
    status = 'success' if proc.returncode == 0 else 'failed'

    with (scratch / f'{now}-{status}.paths').open('w') as fh:
        fh.write('\n'.join(map(str, paths)))
        fh.write('\n')

    log_path = (scratch / f'{now}-{status}.log')
    log_path.write_bytes(proc.stdout)

    print(f'{status.upper()}: wrote {log_path}')

def run_merge(paths):
    # only include shards (exclude meta)
    shards = '\n'.join(str(p) for p in paths if p.suffix == '.zoekt') + '\n'

    # run the merge
    proc = subprocess.run(['zoekt-merge-index', '-'], input=shards.encode('utf-8'), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    # write output and status to a file
    write_status(proc, paths)

    if proc.returncode != 0:
        return

    # move the old shards to the bak dir
    index_dir = paths[0].parent
    (index_dir / '.scratch/bak').mkdir(parents=True, exist_ok=True)
    for p in paths:
        p.rename(index_dir / '.scratch/bak' / p.name)

def main(args):
    parser = argparse.ArgumentParser(description='Manually merge shards.')
    parser.add_argument('--index', default='/data/index', help='directory for search indices')

    args = parser.parse_args(args)

    index_dir = Path(args.index)
    if not index_dir.exists():
        raise Exception(f'index dir {index_dir} does not exist')

    repos = get_shards(index_dir)
    compounds = gen_compounds(repos)
    for compound in compounds:
        print()
        run_merge(compound)

if __name__ == '__main__':
    import sys
    main(sys.argv[1:])
