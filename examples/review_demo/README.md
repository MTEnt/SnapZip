# SnapZip Review Demo

This public-safe fixture demonstrates `snapzip pr` and `pack --mode review` on a tiny Python project.

## Try It

From this directory:

```bash
git init
git add .
git commit -m "baseline review demo"

# Make a small branch-style change before running the review workflow.
printf '\n# temporary review change\n' >> app/cache.py

snapzip index --reset --db-dir /tmp/snapzip-review-demo --langs python --crawl .
snapzip pr --db-dir /tmp/snapzip-review-demo --changed --dir . --limit 5 --budget 8000
snapzip pack --db-dir /tmp/snapzip-review-demo --query "CacheStore review risk tests" --mode review --limit 5 --budget 8000
```

The PR report should show the changed source file, the likely affected test, a suggested Python validation command from `.snapzip/config.toml`, and a review-mode context pack with receipts.
