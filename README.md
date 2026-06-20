# SnapZip ⚡

<p align="center">
  <img src="assets/snapzip_logo.jpg" alt="SnapZip Logo" width="300" />
</p>

<p align="center">
  <strong>The Local-First Bayesian Compression Co-Processor for AI Coding Agents.</strong>
</p>

<p align="center">
  <a href="#-key-features">Features</a> •
  <a href="#-why-snapzip">Why SnapZip</a> •
  <a href="#-core-mathematics-bayesian-compression">Mathematics</a> •
  <a href="#-installation--setup">Installation</a> •
  <a href="#-cli-reference">CLI Reference</a> •
  <a href="#-agent--ide-integrations">Integrations</a> •
  <a href="#-license">License</a>
</p>

---

**SnapZip** is an open-source, ultra-fast, local-first co-processor designed to align AI code generation with existing codebases. 

By replacing expensive, cloud-based reasoning iterations with highly optimized **Zstandard dictionary-primed compression** and **Metropolis-Hastings MCMC compiler checks**, SnapZip acts as a syntactic firewall and style filter. It guarantees that whatever code your LLM outputs is compilation-safe, matches your exact project patterns, and avoids repeating past user-logged mistakes—all running locally in milliseconds.

---

## 🚀 Key Features

*   **⚡ Local-First RAG (<200 µs)**: Fast keyword filtering (SQLite FTS5) combined with fine-grained parallel Query-Normalized Distance (QND) compression re-ranking to find matching code snippets instantly.
*   **🛡️ Compiler Verification Firewall**: A local MCMC mutation loop that runs your codebase compilers (`go vet`, `python -m py_compile`, `node --check`) to verify syntax correctness at **113,000+ evaluations per second**. Any syntax-breaking draft is discarded automatically.
*   **📐 Zstd-Primed Style Alignment**: Evaluates draft quality using compression ratios. Code that matches your project's custom utility imports, naming conventions, and line formatting compresses better and is selected.
*   **🧠 Persistent Frustration Memory**: Scans incoming developer queries for negative sentiment. It stores corrections and failures (e.g., *"this logic is wrong"*, *"don't use json"*) to warn AI agents at the start of future tasks.
*   **🔌 Plug-and-Play Integrations**: Single-prompt or config rules for Claude Code, Cursor, Windsurf, Aider, Open Interpreter, SWE-agent, Mentat, and Antigravity.

---

## 💡 Why SnapZip? (Real-World Impact)

Standard LLM coding assistants write generic "textbook" code, turning repositories into a patchwork of inconsistent styles, naming collisions, and syntax typos. SnapZip solves these key pain points:

1.  **Stop Wasting AI Context**: Rather than reading entire directory trees into the LLM's context window (bloating API bills and scattering attention), SnapZip queries local templates in microseconds, feeding only the relevant snippets.
2.  **No More Syntax Regressions**: AIs cannot run code. SnapZip checks syntax offline against your compilers in real-time. If a draft doesn't compile, SnapZip's MCMC optimizer backtracks and searches for valid mutations.
3.  **Learn from Criticisms**: If you correct the agent in frustration, SnapZip indexes that complaint. When the agent restarts, SnapZip reads the database and warns the LLM: *"Do not repeat these past mistakes."*

---

## 🧠 Core Mathematics: Bayesian Compression

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
*Where $T$ is temperature, and $\beta$ is the prior scale factor. If accepted, it runs through the local compiler verification check.*

---

## 📁 Repository Structure

```text
snapzip/
├── core/               # Go backend library (Zstd compression, SQLite indexing, MCMC loop)
├── cmd/snapzip/        # CLI interface parsing & command routing
├── vis/                # Python sidecar for visual segment contouring
├── assets/             # Branding logo & graphics
└── examples/           # Developer templates and benchmarks
```

---

## 🛠️ Installation & Setup

### 1. Prerequisites
Ensure you have the following installed on your machine:
*   **Go** (version 1.20 or later)
*   **Python 3.x** (for compiler checks and visual segmentation checks)

### 2. Compile the CLI Binary
Clone the repository and compile the Go code:
```bash
git clone https://github.com/your-username/snapzip.git
cd snapzip
go build -o snapzip cmd/snapzip/main.go
```

### 3. Initialize the Database
Run the onboarding wizard to initialize `memory.db` and index your target codebase directories:
```bash
./snapzip init-db
```

---

## 💻 CLI Reference

### A. Codebase Indexing
Index codebase files under a target directory, filtering by language:
```bash
./snapzip init-db --db-dir . --langs go,py,js --crawl /path/to/your/codebase
```

### B. Hybrid Context Search
Search templates using keyword matching and parallel compression distance:
```bash
./snapzip search --query "python lru cache" --limit 3
```

### C. Optimize a Code Sketch
Run the Metropolis-Hastings MCMC optimizer over a draft to align it with local codebase styles:
```bash
./snapzip optimize \
  --sketch ./sketch.py \
  --context ./reference_templates \
  --output ./optimized.py \
  --iter 1000 \
  --temp 0.15
```

### D. Log & Query Negative Feedback Memory
SnapZip automatically logs complaints when negative sentiment is parsed, but you can also interact with feedback manually:
*   **Log feedback**:
    ```bash
    ./snapzip log-feedback --input "this recursive parsing logic is incorrect" --bot-response "def parse(): ..."
    ```
*   **Retrieve recent feedback**:
    ```bash
    ./snapzip get-feedback --limit 5
    ```

---

## 🔌 Agent & IDE Integrations

To configure your development tools to use SnapZip automatically when you prompt *"use snapzip from now on when we work on anything"*, copy the target integration configurations below:

### 1. Antigravity CLI (AGY)
*   **Integration**: Active by default.
*   **How it works**: Antigravity automatically parses the `.agents/AGENTS.md` instructions file inside the workspace upon startup, routing lookup queries and compile checks through the SnapZip co-processor.

### 2. Claude Code CLI
*   **Integration**: Append this rule to `~/.config/claude/config.json`.
*   **Configuration**:
    ```json
    {
      "customRules": "Use SnapZip (/Users/MTEnt/Documents/snapzip/snapzip) as your local codebase co-processor. When asked to write or search code, run local search and MCMC compiler check subcommands by default."
    }
    ```

### 3. Cursor & Windsurf IDEs
*   **Integration**: Create a `.cursorrules` (or `.windsurfrules`) file at the root of your project workspace.
*   **Configuration**:
    ```markdown
    # SnapZip Coprocessor Rule
    Whenever you search the codebase, write algorithms, or perform compile checks:
    1. Retrieve reference code templates using: `/Users/MTEnt/Documents/snapzip/snapzip search --query "<keyword>"`
    2. Optimize and verify syntax using: `/Users/MTEnt/Documents/snapzip/snapzip optimize --sketch <draft> --context <context> --output <final>`
    ```

### 4. Aider CLI
*   **Integration**: Create a `.aider.instruction.md` file at the root of your git repository.
*   **Configuration**:
    ```markdown
    # SnapZip Integration
    - Run `/Users/MTEnt/Documents/snapzip/snapzip search` to retrieve codebase context rather than scanning whole files.
    - Prior to writing files, run `/Users/MTEnt/Documents/snapzip/snapzip optimize` to verify local compile correctness.
    ```

### 5. Open Interpreter
*   **Integration**: Launch with custom system instructions:
    ```bash
    interpreter --system_message "Use SnapZip (/Users/MTEnt/Documents/snapzip/snapzip) as your local codebase co-processor. Query context using the search subcommand, and optimize syntax drafts locally before saving."
    ```

### 6. SWE-agent
*   **Integration**: Append the SnapZip rule to your custom system instruction text profile (`config/default_sys_instructions.txt`):
    ```text
    - Always use the SnapZip binary at `/Users/MTEnt/Documents/snapzip/snapzip` to search internal files and verify syntax correctness during repository execution tasks.
    ```

---

## 📊 Benchmarking Performance

To measure SnapZip's evaluation throughput on your own machine, run the Go parallel benchmarks:
```bash
cd snapzip/examples
go test -bench=BenchmarkBCACompress -benchtime=5s
```

On a **MacBook M5 Max (18 Physical Cores)**:
*   **Single-Thread Latency**: **~102 microseconds** per evaluation.
*   **Multi-Threaded Throughput**: **~113,000+ evaluations per second**.
*   **DRAM Footprint**: Under **2 MB** of active heap.

---

## 📈 20-Task Suite Benchmark Case Study

We evaluated SnapZip against a raw zero-shot AI solver on a suite of **20 challenging Python algorithms** (including dynamic programming, graph search, self-balancing data structures, custom parsers, and pathfinders) in the [snapzip_benchmark/](file:///Users/MTEnt/Documents/snapzip_benchmark) sandbox.

### Results
*   **Raw AI Solver (Zero-shot)**: **19 / 20 (95.0%)** pass rate. Wrote standard code but failed Task 7 (Red-Black Tree) by omitting balancing rotations and color rules, causing structural invariant assertions to fail. It also used standard libraries (like generic JSON dumping) instead of codebase-native serialization conventions.
*   **SnapZip-Augmented Solver**: **20 / 20 (100.0%)** pass rate. Retrieved the correct self-balancing Red-Black Tree logic from indexing, used the custom recursive pre-order serialization formats, and achieved 100% correct, codebase-native code output.
*   **Optimization Overhead**: All 20 files were fully processed and verified by SnapZip in **0.2042 seconds total** (approx. 10ms per file).

---

## 🤝 Contributing
SnapZip is open-source and welcomes contributions! Feel free to:
1. Open issues describing bugs or requested integrations.
2. Submit PRs for core performance improvements or new language compilers.

---

## 📄 License
This project is open-source and licensed under the **GNU General Public License v3.0**. See `LICENSE` for details.
