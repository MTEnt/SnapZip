# Contributing

Thanks for helping improve SnapZip. Keep changes small, tested, and focused on local-first code retrieval, optimization, and agent integration.

## Development Setup

```bash
git clone https://github.com/MTEnt/SnapZip.git
cd SnapZip
go test ./...
go build -o snapzip ./cmd/snapzip
```

SnapZip requires Go 1.25.8 or later so vulnerability scans run against patched standard-library packages.

## Required Checks

Run these before opening a pull request:

```bash
go test ./...
go vet ./...
go test -race ./...
python3 -m py_compile examples/draft_cache.py examples/context_code/cache_reference.py examples/review_demo/app/cache.py examples/review_demo/tests/test_cache.py benchmarks/run.py benchmarks/stress_rbt.py benchmarks/suites/algorithm_20/*.py
$(go env GOPATH)/bin/govulncheck ./...
python3 benchmarks/run.py --suite smoke --snapzip-bin ./snapzip
```

If `govulncheck` is not installed:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
```

## Pull Request Expectations

- Do not commit generated `memory.db` files, benchmark work directories, local sketches, or built binaries.
- Add or update tests for behavior changes.
- Keep CLI changes reflected in `README.md` and `LLM_INSTRUCTIONS.md` when agent usage changes.
- Keep `action.yml` and `.github/workflows/snapzip-pr-context.yml` aligned when PR-context automation changes.
- Prefer simple, explicit Go over clever abstractions.
