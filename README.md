# SnapZip

<p align="center">
  <img src="assets/snapzip_logo.jpg" alt="SnapZip Logo" width="300" />
</p>

<p align="center">
  <strong>Local codebase memory for AI coding agents.</strong>
</p>

<p align="center">
  <a href="#key-features">Features</a> |
  <a href="#why-snapzip">Why SnapZip</a> |
  <a href="#core-model-local-relevance-ranking">Model</a> |
  <a href="#installation--setup">Installation</a> |
  <a href="#cli-reference">CLI Reference</a> |
  <a href="#agent--ide-integrations">Integrations</a> |
  <a href="#license">License</a>
</p>

---

**SnapZip** is an open-source, local-first CLI that helps AI coding agents search relevant examples from your codebase, surface project feedback, and keep repo memory private.

It combines SQLite FTS5 search, path-aware relevance, compression-distance re-ranking, and lightweight local syntax checks. All project memory is generated locally in `memory.db`; the repository does not ship with user memories or indexed code.

---

## Key Features

*   **Local code search**: SQLite FTS5 keyword search with path-aware lexical weighting and Query-Normalized Distance (QND) compression re-ranking.
*   **Language-aware indexing**: Index popular source formats by default, or pass explicit extensions such as `html,css,rb,py,go,rs,zig`. Go uses parser-backed top-level declaration chunks; Python, Ruby, and common brace languages such as JavaScript, TypeScript, Java, C#, C/C++, Rust, PHP, Swift, Kotlin, and Scala use structural top-level chunks when large files need splitting.
*   **Repo maps, symbols, references, imports, and dependency graphs**: Stores file paths, line ranges, content hashes, indexed functions/classes/types, lightweight call/reference sites, and import/dependency references. Local imports are resolved to indexed target files when SnapZip can map them safely.
*   **Task-specific context packs**: Build bounded packs for debug, refactor, test, docs, and review workflows.
*   **Call-Graph Context Expansion (Dependent Type Resolution)**: Scans retrieved code snippets for custom class, struct, and interface references, then automatically includes their type declarations to prevent compilation and type errors in AI-generated code.
*   **Context quality scoring**: Every pack reports receipt coverage, structural graph receipt coverage, evidence density, definition/reference/test coverage, uniqueness, budget use, and warnings.
*   **Multi-path retrieval planning**: Search uses primary terms, expanded identifiers, code-context terms, metadata, and graph signals as separate retrieval paths before rank fusion.
*   **Structural reranking**: Direct symbol/task searches blend lexical/QND ranking with indexed symbols, references, and resolved import evidence so code-shaped queries can favor definitions and callers over keyword-heavy noise.
*   **Diff-aware PR context**: Summarizes changed files, likely affected tests, suggested validation commands, and review-mode context before a branch is finalized.
*   **Validation planning**: Finds likely affected tests, suggests validation commands, and can run a supplied command with repair context on failure.
*   **Project profiles**: Optional `.snapzip/config.toml` lets teams share validation commands without shipping any local memory.
*   **Syntax checks where available**: Uses local toolchains for Go, Python, JavaScript, Ruby, PHP, Perl, Lua, shell, C/C++, Swift, and TypeScript validation during optimization.
*   **Private feedback memory**: Stores negative project feedback locally so agents can avoid repeating known mistakes.
*   **Simple agent integration**: Works as a CLI, JSON-output command, or read-only MCP stdio server for coding agents and editor integrations.

---

## Why SnapZip?

Standard LLM coding assistants often need too much context or fall back to generic examples. SnapZip gives agents a small local memory layer they can query before making changes:

1.  **Use focused local context**: Instead of loading broad directory trees into the LLM context window, SnapZip returns targeted snippets from a local SQLite index.
2.  **Keep memory private**: Indexed snippets and feedback stay in a local `memory.db` file.
3.  **Catch syntax problems earlier**: SnapZip can run local syntax checks for supported languages before an optimized draft becomes final output.
4.  **Remember project feedback**: If you log a correction, SnapZip can surface it before future work.

---

## Core Model: Local Relevance Ranking

SnapZip is primarily a retrieval and local-memory tool. It ranks indexed snippets with a blend of:

*   SQLite FTS5 keyword matches
*   source path and file-type relevance
*   lexical overlap with the query
*   Query-Normalized Distance (QND) compression scoring
*   indexed definitions, lightweight call/reference-site matches, and resolved local import/dependency edges
*   repair-specific stack, symbol, identifier, and file/line signals when using failure workflows

Every context pack includes context receipts when budget allows. Receipts explain why each snippet was included, such as a matched stack frame, matched symbol, matched expanded-identifier retrieval path, resolved local import edge, related test, or fallback retrieval match. Search builds several deterministic retrieval paths from the prompt: primary terms, expanded identifiers such as camelCase/snake_case pieces, compact code-context tokens, indexed metadata, and graph edges. Direct symbol/task searches also use indexed symbols, references, and imports as a bounded structural reranking signal before pack assembly. Multi-line completion context stays on the standard lexical/QND path. When large supported source files are indexed, SnapZip uses structural spans before falling back to boundary heuristics, so snippets are less likely to merge unrelated top-level declarations. When the snippet limit allows it, packs add resolved local import neighbors so agents see the source/test/dependency files that move together. Context packing also automatically resolves Call-Graph dependent types—scanning retrieved snippets for custom class, struct, and interface names (both locally and across external imports) and pulling in their declarations to prevent LLM generation type errors. Task-mode packs (`debug`, `refactor`, `test`, and `review`) also use local symbol-reference edges to attach callers or definitions when a retrieved snippet references project code. Packs include a context quality score with measurable coverage and warning signals. This makes the context auditable for humans and machine-readable for agents.

The optional optimizer is conservative. It uses local code context and Zstandard dictionary scoring, but only mutates files when a local syntax checker is available for that language. Invalid proposals are rejected, and unsupported languages return the seed draft unchanged.

---

## Repository Structure

```text
snapzip/
|-- core/               # Go backend library (SQLite indexing, ranking, compression scoring)
|-- cmd/snapzip/        # CLI interface parsing and command routing
|-- benchmarks/         # Reproducible raw vs SnapZip benchmark harnesses
|-- assets/             # Branding logo and graphics
|-- examples/           # Developer templates, demos, and benchmarks
`-- packaging/          # Homebrew and release packaging scaffolds
```

---

## Installation & Setup

### 1. Prerequisites
Ensure you have the following installed on your machine:
*   **Go** (version 1.25.8 or later)
*   **Python 3.x** (for Python syntax checks and benchmarks)

### 2. Install or Compile the CLI Binary
Install directly from GitHub:
```bash
go install github.com/MTEnt/SnapZip/cmd/snapzip@latest
```

Or clone the repository and compile the Go code:
```bash
git clone https://github.com/MTEnt/SnapZip.git
cd SnapZip
go build -o snapzip ./cmd/snapzip
```

The repository also includes a head-only Homebrew formula scaffold:

```bash
brew install --HEAD ./packaging/homebrew/snapzip.rb
```

Use the formula in `packaging/homebrew/` as the starting point for a dedicated tap after the first tagged release.

### 3. Initialize the Database
If you installed with `go install`, run the CLI as `snapzip`. If you built locally with `go build -o snapzip`, run it as `./snapzip`.

Run the onboarding wizard to initialize a fresh local `memory.db`:
```bash
snapzip init-db
```

Optionally create a project profile for shared validation commands:

```bash
snapzip init-config --dir .
```

---

## CLI Reference

### A. Codebase Indexing
Index codebase files under a target directory, filtering by language name or extension:
```bash
snapzip init-db --db-dir . --langs popular --crawl /path/to/your/codebase
```

Use `index` for repeat indexing and incremental workflows:

```bash
snapzip index --db-dir . --langs all --crawl .
snapzip index --db-dir . --langs all --crawl . --changed
snapzip index --db-dir . --langs all --crawl . --since HEAD~1
snapzip index --db-dir . --langs go --crawl . --max-content-bytes 32768
```

`--langs` accepts presets (`popular`, `web`, `frontend`, `backend`, `mobile`, `systems`, `config`), extensions (`html,css,rb,py,js,rs,zig`), and language names (`ruby,python,javascript,rust`).

Use `popular` for source-heavy indexing. Use `all` or `any` when you also want docs, configs, workflows, and project instructions included in search results. Explicit extensions are accepted even when they are not part of the default common-language list.

Use `--reset` to remove an existing `memory.db` before indexing a fresh project:

```bash
snapzip init-db --db-dir . --langs all --crawl /path/to/your/codebase --reset
```

The indexer skips common dependency/build directories such as `.git`, `node_modules`, `vendor`, `dist`, `build`, `target`, `.venv`, and `__pycache__`. It also skips `memory.db`, binary-looking files, and files larger than 1 MiB by default. Larger accepted source files are split into bounded searchable chunks to keep search reranking responsive. Override the file cap with `--max-file-bytes` and the snippet chunk cap with `--max-content-bytes`.

Add `.snapzipignore` in a project root to exclude additional local-only files or directories before indexing:

```text
# .snapzipignore
private/
*.local.py
scratch/
```

Indexed snippets include source path, line range, content hash, and source modification time. Supported source files also populate local symbol, reference, and import tables used by repo maps and related-file lookup. External packages are indexed as import references, but only imports that map to indexed local files receive a resolved target path.

Common default formats include:

```text
Python, JavaScript, TypeScript, HTML, CSS, Ruby, PHP, Java, C#, C, C++, Go,
Rust, SQL, Swift, Kotlin, Dart, Scala, Lua, Perl, R, Shell, PowerShell, Vue,
Svelte, Astro, Markdown/MDX, JSON/JSONC/JSON5, YAML, TOML, XML, GraphQL,
Dockerfile, Makefile, Terraform/HCL, Protocol Buffers, Solidity, Nix, Zig,
Elixir, Erlang, Clojure, F#, OCaml, Haskell, Julia, and common config files.
```

### B. Hybrid Context Search
Search templates using keyword matching, source-path relevance, and parallel compression distance:
```bash
snapzip search --query "python lru cache" --limit 3
```

Plain-text search output includes context receipts when receipts are available, so graph-expanded or structure-backed results explain why they were included. Use `--json` when an agent or script needs the structured receipt fields directly:

```bash
snapzip search --query "python lru cache" --limit 3 --json
```

### C. Context Packs
Build a bounded Markdown context pack with ranked snippets and relevant feedback memory:
```bash
snapzip pack --query "python lru cache" --limit 5 --budget 12000
```

Every pack includes a context quality section. Treat it as an evidence checklist: it highlights receipt coverage, structural graph receipt coverage, definition/reference/test coverage, evidence density, duplicate paths, dependency snippets, and truncation. Packs also automatically execute Call-Graph Context Expansion (Dependent Type Resolution)—scanning retrieved snippets for custom class, struct, and interface names (both locally and across external imports) and pulling in their declarations to prevent LLM generation type errors. Packs can also include resolved local import neighbors, such as tests importing the source file or source files importing a local module. In task modes, packs can also include local symbol-reference neighbors, such as a test that calls a retrieved function or the source definition called by a retrieved test.

Use a mode when the task has a clear shape:

```bash
snapzip pack --query "cache failure" --mode debug --limit 5 --budget 12000
snapzip pack --query "cache storage" --mode refactor --limit 5 --budget 12000
snapzip pack --query "cache behavior" --mode test --limit 5 --budget 12000
snapzip pack --query "installation" --mode docs --limit 5 --budget 12000
snapzip pack --query "cache diff risk" --mode review --limit 5 --budget 12000
```

Use JSON output when the caller wants structured snippets, receipts, and quality metrics instead of Markdown:

```bash
snapzip pack --query "python lru cache" --limit 5 --budget 12000 --json
```

### D. Repo Map, Symbols, and Graph
Inspect indexed structure:

```bash
snapzip map --db-dir . --limit 50
snapzip symbols --db-dir . --query "CacheStore" --limit 10
snapzip symbol-context --db-dir . --query "CacheStore" --limit 10
snapzip imports --db-dir . --query "app.cache" --limit 10
snapzip graph --db-dir . --path app/cache.py --limit 10
snapzip related --db-dir . --path core/database.go --limit 10
```

Use these commands when an agent needs structural context before editing a file. `symbol-context` returns matching definitions plus indexed call/reference sites. `imports` returns matching module, package, dependency, and linked-asset references. `graph` shows outgoing/imported-by edges plus indexed symbols, in-file references, external callers, and external definitions referenced by the file.

When an import resolves locally, `imports` shows the edge:

```text
tests/test_cache.py:1 [py] app.cache -> app/cache.py | from app.cache import build_cache
```

### E. Failure Context
Build a context pack from failing test/build output:

```bash
snapzip repair-pack --db-dir . --error-file /tmp/test-output.txt --budget 12000
```

Repair packs parse stack frames, file paths, line numbers, symbols, and failure identifiers, then include context receipts explaining the ranking.

```bash
snapzip repair-pack --db-dir . --error-file /tmp/test-output.txt --budget 12000 --json
```

Run a command and build a repair pack from its captured failure output:

```bash
snapzip diagnose --db-dir . --cmd "go test ./..." --budget 12000
```

Find tests likely affected by a changed or named source file:

```bash
snapzip affected --db-dir . --path core/database.go
snapzip affected --db-dir . --changed
```

Plan validation, or run a validation command and build failure context if it fails:

```bash
snapzip validate --db-dir . --path core/database.go
snapzip validate --db-dir . --changed --cmd "go test ./..."
```

If `.snapzip/config.toml` defines a validation command, SnapZip suggests it by default and runs it only when explicitly requested:

```bash
snapzip validate --db-dir . --changed --run-config
```

Build diff-aware review context before finalizing a branch:

```bash
snapzip pr --db-dir . --base origin/main --dir . --limit 10 --budget 16000
snapzip pr --db-dir . --changed --dir . --limit 10 --budget 16000
```

`pr` reports changed files, likely affected tests, suggested validation commands, and a review-mode context pack with receipts and quality metrics.

`explain-failure` is the same workflow as `repair-pack` with a diagnosis-oriented name.

### F. Project Profile, Privacy Audit, and Agent Setup
Create a starter project profile:

```bash
snapzip init-config --dir .
```

Example `.snapzip/config.toml`:

```toml
[validation]
command = "go test ./..."

[validation.commands]
go = "go test ./..."
py = "python -m pytest"
```

Check local index hygiene:

```bash
snapzip audit --db-dir .
```

Generate integration files for common coding-agent surfaces:

```bash
snapzip install-agent codex --dir .
snapzip install-agent claude --dir .
snapzip install-agent cursor --dir .
snapzip install-agent continue --dir .
```

Existing files are skipped unless `--force` is provided.

### G. MCP Server
Run SnapZip as a local read-only MCP stdio server:
```bash
snapzip mcp --db-dir .
```

The MCP server exposes read-only `search`, `context_pack`, `repair_pack`, `affected_tests`, `validation_plan`, `pr_context`, `map`, `symbols`, `symbol_context`, `imports`, `graph`, `related`, `get_feedback`, and `stats` tools. It writes protocol messages to stdout and logs only to stderr, so it can be launched by MCP-compatible clients.

Example client configuration shape:

```json
{
  "mcpServers": {
    "snapzip": {
      "command": "snapzip",
      "args": ["mcp", "--db-dir", "."]
    }
  }
}
```

### H. Inspect Database Stats
Show indexed row counts and language breakdown:
```bash
snapzip stats --db-dir .
```

Use `--json` for structured stats:

```bash
snapzip stats --db-dir . --json
```

Show release/build metadata:

```bash
snapzip version
snapzip version --json
```

### I. Optimize a Code Sketch
Run the conservative optimizer over a draft using local codebase context:
```bash
snapzip optimize \
  --sketch ./examples/draft_cache.py \
  --context ./examples/context_code \
  --output ./optimized_cache.py \
  --iter 1000 \
  --temp 0.15
```

Optimization is conservative: SnapZip only mutates files when a local syntax checker is available for that language, rejects syntactically invalid proposals, and otherwise returns the seed draft unchanged.

### J. Log & Query Negative Feedback Memory
SnapZip does not log search queries into feedback memory. Feedback is only stored when you explicitly call `log-feedback` with a clear negative critique:
*   **Log feedback**:
    ```bash
    snapzip log-feedback --input "this cache eviction logic is incorrect" --bot-response "def put(...): ..."
    ```
*   **Retrieve recent feedback**:
    ```bash
    snapzip get-feedback --limit 5
    ```

---

## Agent & IDE Integrations

Add a project or global agent rule that calls SnapZip when the binary is available:

```text
Use SnapZip when available. Before non-trivial code changes, run `snapzip pack --query "<topic>" --limit 5 --budget 12000 --mode <debug|refactor|test|docs|review>` for targeted local context, receipts, and feedback memory. Use `snapzip map`, `snapzip symbols --query "<symbol>"`, `snapzip symbol-context --query "<symbol>"`, `snapzip imports --query "<module>"`, `snapzip graph --path <file>`, `snapzip related --path <file>`, and `snapzip affected --path <file>` for structural and test context. Before finalizing branch work, run `snapzip pr --changed` or `snapzip pr --base <ref>` for review context. After failing tests, run `snapzip repair-pack --error-file <test-output>` or `snapzip diagnose --cmd "<test command>"`. For generated drafts, run `snapzip optimize --sketch <draft> --context <context_dir> --output <final>` when practical.
```

Use [LLM_INSTRUCTIONS.md](LLM_INSTRUCTIONS.md) as a portable rule template for other agents and editor integrations.

### GitHub Action
SnapZip also ships a composite GitHub Action that builds the CLI from this repository, indexes the checked-out project, runs `snapzip pr`, writes Markdown and JSON reports, and appends the Markdown report to the workflow summary. The action itself uses shell and Go rather than a Node runtime.

```yaml
name: SnapZip PR Context

on:
  pull_request:

permissions:
  contents: read

jobs:
  snapzip:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - id: snapzip
        uses: MTEnt/SnapZip@main
        with:
          base: ${{ github.event.pull_request.base.sha }}
          langs: all
          limit: "10"
          budget: "16000"
      - uses: actions/upload-artifact@v7
        with:
          name: snapzip-pr-context
          path: |
            ${{ steps.snapzip.outputs.report_path }}
            ${{ steps.snapzip.outputs.json_path }}
```

Use `MTEnt/SnapZip@main` for the current development action, then pin to a release tag after publishing a tagged release.

### Public Review Demo
Use the checked-in public fixture to try review context without pointing SnapZip at a private project:

```bash
cd examples/review_demo
git init
git add .
git commit -m "baseline review demo"
printf '\n# temporary review change\n' >> app/cache.py

snapzip index --reset --db-dir /tmp/snapzip-review-demo --langs python --crawl .
snapzip pr --db-dir /tmp/snapzip-review-demo --changed --dir . --limit 5 --budget 8000
snapzip pack --db-dir /tmp/snapzip-review-demo --query "CacheStore review risk tests" --mode review --limit 5 --budget 8000
```

---

## Benchmarking Performance

Build the CLI and run the included benchmark harness:
```bash
go build -o snapzip ./cmd/snapzip
python3 benchmarks/run.py --suite smoke --snapzip-bin ./snapzip
```

Or run the benchmark harness through SnapZip:

```bash
snapzip eval --suite smoke --snapzip-bin ./snapzip
```

Run the repair retrieval quality check:

```bash
python3 benchmarks/run.py --suite repair-retrieval --snapzip-bin ./snapzip
```

Run the context quality check:

```bash
python3 benchmarks/run.py --suite context-quality --snapzip-bin ./snapzip
```

This public-safe suite checks context quality metrics, verifies that task-mode packs include symbol-reference receipts for source/test caller-definition context, confirms `graph --json` exposes symbol caller/definition edges, confirms structural reranking prefers an indexed definition over keyword-heavy noise, gates multi-path retrieval and receipts for expanded identifiers, and gates Go AST-backed, Python structural, plus popular JavaScript/Ruby declaration chunking.

Run the full 20-task algorithm suite:
```bash
python3 benchmarks/run.py --suite algorithm-20 --snapzip-bin ./snapzip
```

Run the public RepoBench-R retrieval sample:
```bash
python3 benchmarks/run.py --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 --json /tmp/snapzip-repobench-r.json
```

The same suite can be run through the CLI wrapper:

```bash
snapzip eval --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 --json /tmp/snapzip-repobench-r.json
```

Run the public Python/Java, easy/hard matrix:

```bash
python3 benchmarks/run.py --suite repobench-r-matrix --snapzip-bin ./snapzip \
  --repobench-sample-size 25 \
  --json /tmp/snapzip-repobench-r-matrix-smoke.json
```

Use `--repobench-sample-size 0` for a full split sweep. If you pass local RepoBench-R data for matrix mode, pass a directory containing the public `data/<config>.gz` files rather than one `.gz` file.

Current 100-sample `python_cff` / `test_hard` readout, seed `42`:

- Jaccard: 10/100 acc@1, 32/100 acc@3, 48/100 acc@5, 0.2315 MRR@5, 0.292862 nDCG@5
- BM25: 14/100 acc@1, 31/100 acc@3, 52/100 acc@5, 0.261167 MRR@5, 0.324596 nDCG@5
- SnapZip: 17/100 acc@1, 36/100 acc@3, 59/100 acc@5, 0.298667 MRR@5, 0.369709 nDCG@5

For release checks, add benchmark quality gates:
```bash
python3 benchmarks/run.py --suite repobench-r --snapzip-bin ./snapzip --repobench-sample-size 100 \
  --min-repobench-snapzip-acc1 0.17 \
  --min-repobench-snapzip-acc3 0.36 \
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

Run the public RepoBench v1.1 pipeline-context proxy:
```bash
python3 -m pip install "huggingface_hub>=0.23" "pyarrow>=15"
python3 benchmarks/run.py --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 --json /tmp/snapzip-repobench-p.json
```

Or through the CLI wrapper:

```bash
snapzip eval --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 --json /tmp/snapzip-repobench-p.json
```

This suite uses the public RepoBench v1.1 Python `cross_file_first` split. It does not call a live model or report completion accuracy. It measures whether the retrieved top-5 context contains the gold cross-file snippet and covers next-line tokens that were absent from the raw in-file prompt.

Current 100-sample first-shard readout, seed `42`:

- Random: 84/100 gold hit@5, 0.506 MRR@5, 0.293 new-token coverage@5
- Jaccard: 90/100 gold hit@5, 0.564333 MRR@5, 0.309667 new-token coverage@5
- BM25: 89/100 gold hit@5, 0.569667 MRR@5, 0.300667 new-token coverage@5, 0.92 identifier hit@5
- SnapZip: 32/100 gold hit@1, 75/100 gold hit@3, 89/100 gold hit@5, 0.544667 MRR@5, 0.307167 new-token coverage@5, 0.93 identifier hit@5

Optional quality gates for this proxy:
```bash
python3 benchmarks/run.py --suite repobench-p --snapzip-bin ./snapzip --repobench-p-sample-size 100 \
  --min-repobench-p-snapzip-gold-hit5 0.89 \
  --min-repobench-p-snapzip-new-token-coverage5 0.307167 \
  --min-repobench-p-snapzip-identifier-hit5 0.93 \
  --min-repobench-p-snapzip-new-token-coverage5-over-bm25 0.006
```

Run a live-model raw vs SnapZip-assisted completion sample:
```bash
export SNAPZIP_LIVE_CLI_CMD='your-model-cli-command'
python3 benchmarks/run.py --suite repobench-live --snapzip-bin ./snapzip \
  --live-cli-cmd "$SNAPZIP_LIVE_CLI_CMD" \
  --live-model your-model-label \
  --live-sample-size 20 \
  --json /tmp/snapzip-repobench-live.json
```

This suite uses the same public RepoBench v1.1 completion rows, calls the selected local model CLI twice per case, and compares exact next-line completion from the raw in-file prompt against the same prompt with SnapZip-selected cross-file context. The CLI command receives the prompt on stdin by default; commands may also use `{prompt}` or `{prompt_file}` placeholders. It reports exact match, trimmed exact match, token F1, model latency, and SnapZip indexing/search latency. It is intentionally not part of `--suite all` so routine local benchmark runs do not call an external model accidentally.

Run all benchmark suites and write a JSON report:
```bash
python3 benchmarks/run.py --suite all --snapzip-bin ./snapzip --json /tmp/snapzip-benchmark.json
```

For low-level compression throughput, run the Go benchmarks:
```bash
go test -bench=BenchmarkBCACompress -benchtime=5s ./examples
```

These benchmarks validate SnapZip's local retrieval, repair-context ranking, and syntax-check workflow against included public-safe harnesses. They are useful for regression testing, but they are not universal claims about every codebase or every AI coding task. If you publish numbers, include the exact command, SnapZip commit, machine details, and JSON report.

---

## Contributing
SnapZip is open-source and welcomes contributions. See [CONTRIBUTING.md](CONTRIBUTING.md) for development checks and pull request expectations.

---

## Security
Please report suspected vulnerabilities privately. See [SECURITY.md](SECURITY.md).

---

## License
This project is open-source and licensed under the **GNU General Public License v3.0**. See `LICENSE` for details.
