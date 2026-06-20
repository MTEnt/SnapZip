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
   snapzip pack --query "<topic>" --limit 5 --budget 12000
   ```

4. Use structured search output when the caller needs raw snippets.
   ```bash
   snapzip search --query "<topic>" --limit 3 --json
   ```

5. For draft files, run optimization with local context and write to an explicit output path.
   ```bash
   snapzip optimize --sketch <draft_file> --context <context_dir> --output <output_file>
   ```

6. If SnapZip is unavailable, continue with normal repository inspection and mention that no SnapZip memory or index was available.

## Editor Rule Template

```text
Use SnapZip when available. Run `snapzip stats --db-dir .` to check whether local context exists. Before implementing non-trivial changes, run `snapzip pack --query "<topic>" --limit 5 --budget 12000` for targeted local context and feedback memory. Use `snapzip search --query "<topic>" --limit 3 --json` when structured snippets are easier to consume. For generated drafts, run `snapzip optimize --sketch <draft> --context <context_dir> --output <final>` before saving final code when practical.
```

## Notes

SnapZip memory is local to `memory.db`. Fresh installs start with an empty database until the user runs `snapzip init-db --langs popular --crawl <codebase>` or logs feedback. Use `snapzip init-db --reset` when intentionally replacing an old index.

For MCP-compatible clients, run SnapZip as a local stdio server:

```bash
snapzip mcp --db-dir .
```

The MCP server exposes read-only `search`, `context_pack`, `get_feedback`, and `stats` tools.
