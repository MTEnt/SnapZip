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

## Repair Retrieval Benchmark

The repair retrieval suite uses a public-safe synthetic failure that resembles a unit-test traceback. It checks whether `repair-pack` ranks the target source function first and emits context receipts that explain the ranking.

```bash
python3 benchmarks/run.py --suite repair-retrieval --snapzip-bin ./snapzip
```

## Context Quality Benchmark

The context quality suite uses public-safe synthetic projects with source, tests, distracting cache-related noise, a structural reranking fixture, a multi-path expanded-identifier retrieval fixture, and Go/Python/JavaScript/Ruby chunking fixtures. It checks whether `pack --json` emits quality metrics for receipts, structural graph receipts, definitions, references, and test coverage. It also verifies that task-mode packs include local symbol-reference graph receipts that explain source/test caller-definition relationships, that `graph --json` exposes symbol caller/definition edges, that structural reranking prefers an indexed definition over keyword-heavy noise, that multi-path retrieval can recover split identifiers from camelCase prompt terms and explain the matched expanded-identifier terms in receipts, and that structural chunking keeps unrelated top-level declarations out of focused snippets.

```bash
python3 benchmarks/run.py --suite context-quality --snapzip-bin ./snapzip
```

## RepoBench-R Retrieval Benchmark

The RepoBench-R suite uses the public `tianyang/repobench-r` dataset. It materializes the official candidate snippets into temporary files, indexes them with SnapZip, and compares top-5 retrieval against token Jaccard and BM25 baselines.
The report includes acc@1/3/5, MRR@5, nDCG@5, gold-rank diagnostics, and duplicate top-5 result counts.

```bash
python3 benchmarks/run.py --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 --json /tmp/snapzip-repobench-r.json
```

Equivalent CLI wrapper:

```bash
snapzip eval --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 --json /tmp/snapzip-repobench-r.json
```

Run the public matrix across Python/Java and easy/hard splits:

```bash
python3 benchmarks/run.py --suite repobench-r-matrix --snapzip-bin ./snapzip \
  --repobench-sample-size 25 \
  --json /tmp/snapzip-repobench-r-matrix-smoke.json
```

For a full split sweep, use `--repobench-sample-size 0`. If you pass local data for matrix mode, pass a directory containing `data/python_cff.gz`, `data/python_cfr.gz`, `data/java_cff.gz`, and `data/java_cfr.gz`, not a single `.gz` file.

Current 100-sample public readout on `python_cff` / `test_hard`, seed `42`:

- Jaccard: 10/100 acc@1, 32/100 acc@3, 48/100 acc@5, 0.2315 MRR@5, 0.292862 nDCG@5
- BM25: 14/100 acc@1, 31/100 acc@3, 52/100 acc@5, 0.261167 MRR@5, 0.324596 nDCG@5
- SnapZip: 17/100 acc@1, 34/100 acc@3, 59/100 acc@5, 0.3005 MRR@5, 0.370936 nDCG@5, 0 duplicate top-5 records

Use optional quality gates for release or CI checks:

```bash
python3 benchmarks/run.py --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 \
  --min-repobench-snapzip-acc1 0.17 \
  --min-repobench-snapzip-acc3 0.34 \
  --min-repobench-snapzip-acc5 0.59 \
  --min-repobench-snapzip-mrr5 0.298667 \
  --min-repobench-snapzip-ndcg5 0.369709 \
  --max-repobench-snapzip-duplicate-top5-records 0 \
  --max-repobench-snapzip-duplicate-top5-slots 0 \
  --min-repobench-snapzip-acc5-over-bm25 0.06 \
  --min-repobench-snapzip-mrr5-over-bm25 0.03 \
  --min-repobench-snapzip-ndcg5-over-bm25 0.04 \
  --min-repobench-snapzip-acc5-over-jaccard 0.10
```

The default CI workflow runs this gated public sample so retrieval changes must preserve both the current measured floor and the current measured lift over raw baselines before merging.

Add `--snapzip-diagnostics` to RepoBench-R or RepoBench-P runs when tuning ranking. The JSON records will include compact score diagnostics for SnapZip's top-5 results, including QND, lexical/BM25/BM25F boosts, identifier/path/structure boosts, rank-fusion contribution, final rank, and matched query tokens.

Use the offline tuner to test alternate score-feature weights against those returned candidates:

```bash
python3 benchmarks/run.py --suite repobench-r --snapzip-bin ./snapzip \
  --repobench-sample-size 100 \
  --snapzip-diagnostics \
  --snapzip-search-limit 20 \
  --json /tmp/snapzip-repobench-r-diagnostics.json

python3 benchmarks/tune_diagnostics.py \
  --input /tmp/snapzip-repobench-r-diagnostics.json \
  --metric mrr@5 \
  --json /tmp/snapzip-repobench-r-tuning.json
```

The tuner only reorders candidates SnapZip already returned. Keep the default `--snapzip-search-limit 5` when reproducing published top-5 numbers, and raise it during offline diagnostics when you want to see whether lower-ranked candidates can be promoted into the top five.

## RepoBench v1.1 Pipeline-Context Proxy

The `repobench-p` suite uses the public RepoBench v1.1 parquet dataset. It materializes each row's cross-file context snippets, indexes them with SnapZip, and compares top-5 context selection against random, token Jaccard, and BM25 baselines.

This is not a live-model completion benchmark. It is a context-quality proxy for completion: the report measures gold cross-file snippet hit@1/3/5, MRR@5, nDCG@5, gold-identifier hit@5, and coverage of next-line tokens that are absent from the raw in-file prompt.

```bash
python3 -m pip install "huggingface_hub>=0.23" "pyarrow>=15"
python3 benchmarks/run.py --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 --json /tmp/snapzip-repobench-p.json
```

Equivalent CLI wrapper:

```bash
snapzip eval --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 --json /tmp/snapzip-repobench-p.json
```

Current 100-sample public readout on RepoBench v1.1 Python `cross_file_first`, first parquet shard, seed `42`:

- Random: 84/100 gold hit@5, 0.506 MRR@5, 0.293 new-token coverage@5
- Jaccard: 90/100 gold hit@5, 0.564333 MRR@5, 0.309667 new-token coverage@5
- BM25: 89/100 gold hit@5, 0.569667 MRR@5, 0.300667 new-token coverage@5, 0.92 identifier hit@5
- SnapZip: 32/100 gold hit@1, 75/100 gold hit@3, 89/100 gold hit@5, 0.544667 MRR@5, 0.307167 new-token coverage@5, 0.93 identifier hit@5

Use optional gates when tuning completion-context retrieval:

```bash
python3 benchmarks/run.py --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 \
  --min-repobench-p-snapzip-gold-hit5 0.89 \
  --min-repobench-p-snapzip-new-token-coverage5 0.307167 \
  --min-repobench-p-snapzip-identifier-hit5 0.93 \
  --min-repobench-p-snapzip-new-token-coverage5-over-bm25 0.006
```

The default CI workflow runs a lighter 50-sample proxy gate:

```bash
python3 benchmarks/run.py --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 50 \
  --min-repobench-p-snapzip-gold-hit5 0.90 \
  --min-repobench-p-snapzip-new-token-coverage5 0.26 \
  --min-repobench-p-snapzip-identifier-hit5 0.95 \
  --min-repobench-p-snapzip-new-token-coverage5-over-bm25 0.00
```

## RepoBench v1.1 Live Completion

The `repobench-live` suite runs a local model CLI on the public RepoBench v1.1 completion rows. Each case makes two model calls:

- raw: import statements plus cropped in-file code before the cursor
- SnapZip-assisted: the same prompt plus top-k cross-file context selected by SnapZip

The suite scores the first returned line against RepoBench's `next_line` with exact match, trimmed exact match, and token F1. It is not included in `--suite all` because it calls an external model.

```bash
export SNAPZIP_LIVE_CLI_CMD='your-model-cli-command'
python3 benchmarks/run.py --suite repobench-live --snapzip-bin ./snapzip \
  --live-cli-cmd "$SNAPZIP_LIVE_CLI_CMD" \
  --live-model your-model-label \
  --live-sample-size 20 \
  --json /tmp/snapzip-repobench-live.json
```

Equivalent CLI wrapper:

```bash
snapzip eval --suite repobench-live --snapzip-bin ./snapzip \
  --live-cli-cmd "$SNAPZIP_LIVE_CLI_CMD" \
  --live-model your-model-label \
  --live-sample-size 20 \
  --json /tmp/snapzip-repobench-live.json
```

The model command receives the full prompt on stdin by default. If your CLI cannot read stdin, use `{prompt}` for an inline shell-quoted prompt or `{prompt_file}` for a temporary prompt file path.

By default, live responses are cached under `benchmarks/.work/live-model-cache.json` to avoid paying for repeated identical calls. Use `--live-no-cache` for a fresh uncached run.

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

These benchmarks validate SnapZip's local retrieval, repair-context ranking, context priming, optimizer, and syntax-check workflow. They are not a claim that SnapZip replaces a general code generator or solves arbitrary tasks without reference context. For marketing or release notes, publish the raw JSON report, machine details, SnapZip commit, and exact command used.
