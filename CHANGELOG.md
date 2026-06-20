# Changelog

All notable changes to SnapZip are documented here.

## Unreleased

- Added broad default language support for common source, web, config, and systems formats.
- Added deterministic indexing safeguards for dependency folders, generated output, oversized files, binary files, and duplicate rows.
- Added CLI database reset and stats commands.
- Added reproducible raw versus SnapZip benchmark suites.
- Added CI, release packaging, dependency updates, and project governance docs.
- Added a composite GitHub Action and PR workflow for generating SnapZip review context reports.
- Made optimization conservative by rejecting syntactically invalid proposals and returning the seed when no local syntax checker is available.
- Stopped search queries from writing to feedback memory and narrowed feedback sentiment matching.
- Split indexed source into bounded chunks to reduce search reranking compression overhead.
- Improved search relevance by sanitizing FTS query tokens, removing hard language filtering, and preferring source paths unless queries ask for tests, docs, or workflows.
- Removed the disconnected visual parser sidecar.
- Raised the minimum Go version to 1.25.8 to avoid vulnerable standard-library versions in CI.

## 0.1.0

- Initial public release candidate.
