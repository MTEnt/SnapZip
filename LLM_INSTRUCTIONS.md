# SnapZip Agent Integration

Use this file as a starting point for agent, editor, or shell rules that should call SnapZip during coding work.

## Core Rule

When `snapzip` is available in the workspace or on `PATH`:

1. Confirm that local context exists when starting in a new workspace.
   ```bash
   snapzip stats --db-dir .
   ```

2. Read recent project feedback before planning non-trivial code changes.
   ```bash
   snapzip get-feedback --limit 5
   ```

3. Build a bounded context pack instead of loading broad directory trees.
   ```bash
   snapzip pack --query "<topic>" --limit 5 --budget 12000 --mode <debug|refactor|test|docs>
   ```
   Prefer JSON when the caller needs machine-readable snippets and context receipts.
   ```bash
   snapzip pack --query "<topic>" --limit 5 --budget 12000 --json
   ```

4. Use structural lookup before editing files when symbol or ownership context matters.
   ```bash
   snapzip map --limit 50
   snapzip symbols --query "<symbol>" --limit 10
   snapzip symbol-context --query "<symbol>" --limit 10
   snapzip related --path <file> --limit 10
   ```

5. Use structured search output when the caller needs raw snippets.
   ```bash
   snapzip search --query "<topic>" --limit 3 --json
   ```

6. After failing tests or builds, build a repair context pack from the captured output.
   ```bash
   snapzip repair-pack --error-file <test-output-file> --budget 12000
   ```
   Or run the command through SnapZip and let it capture the failure output.
   ```bash
   snapzip diagnose --cmd "<test command>" --budget 12000
   ```

7. Before or after edits, ask for likely affected tests.
   ```bash
   snapzip affected --path <file> --limit 10
   snapzip affected --changed --limit 10
   ```

8. Plan or run validation before finishing a non-trivial change.
   ```bash
   snapzip validate --path <file> --limit 10
   snapzip validate --changed --cmd "<test command>"
   ```

9. For draft files, run optimization with local context and write to an explicit output path.
   ```bash
   snapzip optimize --sketch <draft_file> --context <context_dir> --output <output_file>
   ```

10. If SnapZip is unavailable, continue with normal repository inspection and mention that no SnapZip memory or index was available.

## Editor Rule Template

```text
Use SnapZip when available. Run `snapzip stats --db-dir .` to check whether local context exists. Before implementing non-trivial changes, run `snapzip pack --query "<topic>" --limit 5 --budget 12000 --mode <debug|refactor|test|docs>` for targeted local context, receipts, and feedback memory. Use `snapzip map`, `snapzip symbols --query "<symbol>"`, `snapzip symbol-context --query "<symbol>"`, `snapzip related --path <file>`, and `snapzip affected --path <file>` for structural and test context. Use `snapzip validate --path <file>` to plan validation, or `snapzip validate --changed --cmd "<test command>"` to run validation before finishing. After failing tests, run `snapzip repair-pack --error-file <test-output>` or `snapzip diagnose --cmd "<test command>"`. For generated drafts, run `snapzip optimize --sketch <draft> --context <context_dir> --output <final>` before saving final code when practical.
```

## Notes

SnapZip memory is local to `memory.db`. Fresh installs start with an empty database until the user runs `snapzip index --langs all --crawl .`, `snapzip init-db --langs popular --crawl <codebase>`, or logs feedback. Use `snapzip init-db --reset` when intentionally replacing an old index.

For MCP-compatible clients, run SnapZip as a local stdio server:

```bash
snapzip mcp --db-dir .
```

The MCP server exposes read-only `search`, `context_pack`, `repair_pack`, `affected_tests`, `validation_plan`, `map`, `symbols`, `symbol_context`, `related`, `get_feedback`, and `stats` tools.
