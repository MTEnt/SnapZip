# SnapZip

<p align="center">
  <img src="assets/snapzip_logo.jpg" alt="SnapZip Logo" width="300" />
</p>

<p align="center">
  <strong>A local codebase context and verification helper for AI coding agents.</strong>
</p>

<p align="center">
  <a href="#key-features">Features</a> |
  <a href="#why-snapzip">Why SnapZip</a> |
  <a href="#core-model-compression-guided-search">Model</a> |
  <a href="#installation--setup">Installation</a> |
  <a href="#cli-reference">CLI Reference</a> |
  <a href="#agent--ide-integrations">Integrations</a> |
  <a href="#license">License</a>
</p>

---

**SnapZip** is an open-source, local-first CLI that helps AI coding agents retrieve codebase-specific examples, check syntax locally, and remember project feedback in a private SQLite database.

It combines SQLite FTS5 search, compression-distance re-ranking, Zstandard dictionary scoring, and lightweight compiler checks. All project memory is generated locally in `memory.db`; the repository does not ship with user memories or indexed code.

---

## Key Features

*   **Local code search**: SQLite FTS5 keyword search with Query-Normalized Distance (QND) compression re-ranking.
*   **Language-aware indexing**: Index popular source formats by default, or pass explicit extensions such as `html,css,rb,py,go,rs,zig`.
*   **Syntax checks where available**: Uses local toolchains for Go, Python, JavaScript, Ruby, PHP, Perl, Lua, shell, C/C++, Swift, and TypeScript validation during optimization.
*   **Private feedback memory**: Stores negative project feedback locally so agents can avoid repeating known mistakes.
*   **Simple agent integration**: Works as a CLI that can be called from coding agents, editor rules, or shell scripts.

---

## Why SnapZip?

Standard LLM coding assistants write generic "textbook" code, turning repositories into a patchwork of inconsistent styles, naming collisions, and syntax typos. SnapZip solves these key pain points:

1.  **Stop Wasting AI Context**: Rather than reading entire directory trees into the LLM's context window (bloating API bills and scattering attention), SnapZip queries local templates in microseconds, feeding only the relevant snippets.
2.  **Catch Syntax Problems Earlier**: SnapZip can run local syntax checks for supported languages before a draft becomes final output.
3.  **Learn from Project Feedback**: If you log a correction, SnapZip keeps it in the local database and can surface it before future work.

---

## Core Model: Compression-Guided Search

Instead of generating text left-to-right (which gets stuck in repetitive loops), SnapZip treats code generation as a physical simulation over complete drafts $X$:

### 1. Likelihood $P(X \mid \text{Codebase})$
We estimate the likelihood of a draft matching the codebase's style by calculating its compressed byte-length $C_{dict}(X)$ under Zstd primed with the local context dictionary:
$$P(X \mid \text{Codebase}) \propto \exp(-C_{dict}(X))$$

### 2. Prior $P(X)$
Penalizes syntactically invalid constructs (such as unmatched brackets or parentheses):
$$P(X) \propto \text{GrammarScore}(X)$$

### 3. Metropolis-Hastings Proposal Acceptance
To transition from draft $X$ to mutated draft $X'$, the mutation is accepted with probability $\alpha$:
$$\alpha = \min\left(1, \exp\left(-\frac{\Delta C}{T}\right)\right)$$
$$\Delta C = [C_{dict}(X') + \beta L_{prior}(X')] - [C_{dict}(X) + \beta L_{prior}(X)]$$
*Where $T$ is temperature, and $\beta$ is the prior scale factor. Candidate improvements can then be checked with available local language tooling.*

---

## Repository Structure

```text
snapzip/
|-- core/               # Go backend library (Zstd compression, SQLite indexing, MCMC loop)
|-- cmd/snapzip/        # CLI interface parsing and command routing
|-- benchmarks/         # Reproducible raw vs SnapZip benchmark harnesses
|-- assets/             # Branding logo and graphics
`-- examples/           # Developer templates and benchmarks
```

---

## Installation & Setup

### 1. Prerequisites
Ensure you have the following installed on your machine:
*   **Go** (version 1.25 or later)
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

### 3. Initialize the Database
Run the onboarding wizard to initialize a fresh local `memory.db` and index your target codebase directories:
```bash
./snapzip init-db
```

---

## CLI Reference

### A. Codebase Indexing
Index codebase files under a target directory, filtering by language name or extension:
```bash
./snapzip init-db --db-dir . --langs popular --crawl /path/to/your/codebase
```

`--langs` accepts presets (`popular`, `web`, `frontend`, `backend`, `mobile`, `systems`, `config`), extensions (`html,css,rb,py,js,rs,zig`), and language names (`ruby,python,javascript,rust`). Use `all` or `any` to index the full default source-code set. Explicit extensions are accepted even when they are not part of the default common-language list.

Use `--reset` to remove an existing `memory.db` before indexing a fresh project:

```bash
./snapzip init-db --db-dir . --langs popular --crawl /path/to/your/codebase --reset
```

The indexer skips common dependency/build directories such as `.git`, `node_modules`, `vendor`, `dist`, `build`, `target`, `.venv`, and `__pycache__`. It also skips `memory.db`, binary-looking files, and files larger than 1 MiB by default. Larger accepted source files are split into bounded searchable chunks to keep search reranking responsive. Override the file cap with `--max-file-bytes`.

Common default formats include:

```text
Python, JavaScript, TypeScript, HTML, CSS, Ruby, PHP, Java, C#, C, C++, Go,
Rust, SQL, Swift, Kotlin, Dart, Scala, Lua, Perl, R, Shell, PowerShell, Vue,
Svelte, Astro, Markdown/MDX, JSON/JSONC/JSON5, YAML, TOML, XML, GraphQL,
Dockerfile, Makefile, Terraform/HCL, Protocol Buffers, Solidity, Nix, Zig,
Elixir, Erlang, Clojure, F#, OCaml, Haskell, Julia, and common config files.
```

### B. Hybrid Context Search
Search templates using keyword matching and parallel compression distance:
```bash
./snapzip search --query "python lru cache" --limit 3
```

### C. Inspect Database Stats
Show indexed row counts and language breakdown:
```bash
./snapzip stats --db-dir .
```

### D. Optimize a Code Sketch
Run the Metropolis-Hastings MCMC optimizer over a draft to align it with local codebase styles:
```bash
./snapzip optimize \
  --sketch ./examples/draft_cache.py \
  --context ./examples/context_code \
  --output ./optimized_cache.py \
  --iter 1000 \
  --temp 0.15
```

Optimization is conservative: SnapZip only mutates files when a local syntax checker is available for that language, rejects syntactically invalid proposals, and otherwise returns the seed draft unchanged.

### E. Log & Query Negative Feedback Memory
SnapZip does not log search queries into feedback memory. Feedback is only stored when you explicitly call `log-feedback` with a clear negative critique:
*   **Log feedback**:
    ```bash
    ./snapzip log-feedback --input "this cache eviction logic is incorrect" --bot-response "def put(...): ..."
    ```
*   **Retrieve recent feedback**:
    ```bash
    ./snapzip get-feedback --limit 5
    ```

---

## Agent & IDE Integrations

Add a project or global agent rule that calls SnapZip when the binary is available:

```text
Use SnapZip when available. Before non-trivial code changes, run `snapzip get-feedback --limit 5`. Use `snapzip search --query "<topic>" --limit 3` for targeted local examples. For generated drafts, run `snapzip optimize --sketch <draft> --context <context_dir> --output <final>` when practical.
```

Use [LLM_INSTRUCTIONS.md](LLM_INSTRUCTIONS.md) as a portable rule template for other agents and editor integrations.

---

## Benchmarking Performance

Build the CLI and run the reproducible benchmark runner:
```bash
go build -o snapzip ./cmd/snapzip
python3 benchmarks/run.py --suite smoke --snapzip-bin ./snapzip
```

Run the full 20-task algorithm suite:
```bash
python3 benchmarks/run.py --suite algorithm-20 --snapzip-bin ./snapzip
```

Run all benchmark suites and write a JSON report:
```bash
python3 benchmarks/run.py --suite all --snapzip-bin ./snapzip --json /tmp/snapzip-benchmark.json
```

For low-level compression throughput, run the Go benchmarks:
```bash
go test -bench=BenchmarkBCACompress -benchtime=5s ./examples
```

Performance depends on CPU, Go version, dictionary size, and installed local language tools. Publish benchmark results with the exact command, SnapZip commit, machine details, and JSON report.

---

## Contributing
SnapZip is open-source and welcomes contributions. See [CONTRIBUTING.md](CONTRIBUTING.md) for development checks and pull request expectations.

---

## Security
Please report suspected vulnerabilities privately. See [SECURITY.md](SECURITY.md).

---

## License
This project is open-source and licensed under the **GNU General Public License v3.0**. See `LICENSE` for details.
