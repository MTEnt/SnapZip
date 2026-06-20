# SnapZip Agent Rules & Guidelines

## SnapZip Memory
When the `snapzip` binary is available in this workspace, inspect the local index and retrieve recent project feedback before code changes:
```bash
./snapzip stats --db-dir .
```

```bash
./snapzip get-feedback --limit 10
```

Use targeted search for local examples instead of reading broad directory trees into context:
```bash
./snapzip search --query "<topic>" --limit 3
```

If the command fails because the binary or database has not been created yet, continue normally and report that SnapZip memory was unavailable.

---

## High-Impact Daily Coding Rules
Always adhere to the following software engineering rules in this workspace:
1. **Inspect before editing**: Read and review target file contents before making any edits.
2. **Keep changes small**: Keep edits highly targeted and atomic to avoid unnecessary code churn or unintended side effects.
3. **Test with real assertions**: Avoid stubbing, mocking, or bypassing test logic. Validate changes using real assertions and real execution tests.
4. **Verify the visible workflow**: Confirm that the end-to-end functionality of the system behaves correctly.
5. **Protect secrets/data**: Never hardcode, expose, or commit API keys, private tokens, or user data.
6. **Preserve compatibility**: Maintain backward compatibility for all existing public APIs, interfaces, config settings, and data structures.
7. **Report the final outcome**: End each task by explaining precisely what changes were made, the rationale behind them, and how they were verified. Avoid code stubs or comments like "TODO: Implement".
