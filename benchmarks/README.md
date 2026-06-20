# SnapZip Benchmarks

This directory contains reproducible benchmark harnesses that compare:

- a raw baseline implementation written directly into task files
- a SnapZip-assisted implementation that runs the local `snapzip optimize` command against reference context

The benchmark runner always works in temporary directories by default, so it does not create or commit `memory.db`, generated task files, or local test output.

## Prerequisites

Build the CLI before running benchmarks:

```bash
go build -o snapzip ./cmd/snapzip
```

## Smoke Benchmark

The smoke suite runs the hard Red-Black Tree stress benchmark. The raw baseline intentionally lacks balancing fixups, while the SnapZip path uses the reference context and optimizer.

```bash
python3 benchmarks/run.py --suite smoke --snapzip-bin ./snapzip
```

## Full Algorithm Benchmark

The 20-task suite covers common algorithm exercises such as LRU cache, trie wildcard search, graph shortest paths, dynamic programming, heapsort, A*, and Red-Black Tree insertion/deletion.

```bash
python3 benchmarks/run.py --suite algorithm-20 --snapzip-bin ./snapzip
```

Run every benchmark and write a JSON report:

```bash
python3 benchmarks/run.py --suite all --snapzip-bin ./snapzip --json /tmp/snapzip-benchmark.json
```

## Interpreting Results

These benchmarks validate SnapZip's local retrieval, context priming, optimizer, and syntax-check workflow. They are not a claim that SnapZip replaces a general code generator or solves arbitrary tasks without reference context. For marketing or release notes, publish the raw JSON report, machine details, SnapZip commit, and exact command used.
