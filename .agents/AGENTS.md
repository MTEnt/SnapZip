# SnapZip Agent Rules & Guidelines

## Memory & Negative Feedback Invariant
Whenever you start a task in this workspace, you MUST run the following command to retrieve recent user frustrations, failures, and negative feedback from the database:
```bash
./snapzip get-feedback --limit 10
```

### Guidance:
1. Review the retrieved feedback to identify specific syntax patterns, bugs, or behaviors that frustrated the user.
2. Under no circumstances should you repeat these identified failure modes or patterns.
3. If the feedback indicates a recurring logical error (e.g. standard BST instead of balanced RBT, or using generic JSON instead of custom serialization), ensure your proposed plan explicitly avoids it.

---

## 🛠️ High-Impact Daily Coding Rules
Always adhere to the following software engineering rules in this workspace:
1. **Inspect before editing**: Read and review target file contents using `view_file` to fully grasp context and existing interfaces before making any edits.
2. **Keep changes small**: Keep edits highly targeted and atomic to avoid unnecessary code churn or unintended side effects.
3. **Test with real assertions**: Avoid stubbing, mocking, or bypassing test logic. Validate changes using real assertions and real execution tests.
4. **Verify the visible workflow**: Confirm that the end-to-end functionality of the system behaves correctly.
5. **Protect secrets/data**: Never hardcode, expose, or commit API keys, private tokens, or user data.
6. **Preserve compatibility**: Maintain backward compatibility for all existing public APIs, interfaces, config settings, and data structures.
7. **Report the final outcome**: End each task by explaining precisely what changes were made, the rationale behind them, and how they were verified. Avoid code stubs or comments like "TODO: Implement".

