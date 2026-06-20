# Changelog

All notable changes to SnapZip are documented here.

## Unreleased

- Added broad default language support for common source, web, config, and systems formats.
- Added deterministic indexing safeguards for dependency folders, generated output, oversized files, binary files, and duplicate rows.
- Added CLI database reset and stats commands.
- Added reproducible raw versus SnapZip benchmark suites.
- Added CI, release packaging, dependency updates, and project governance docs.
- Made optimization conservative by rejecting syntactically invalid proposals and returning the seed when no local syntax checker is available.
- Stopped search queries from writing to feedback memory and narrowed feedback sentiment matching.
- Split indexed source into bounded chunks to reduce search reranking compression overhead.
- Removed the disconnected visual parser sidecar.

## 0.1.0

- Initial public release candidate.
