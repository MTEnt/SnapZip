# Homebrew Packaging

This directory contains the Homebrew formula used by the `MTEnt/homebrew-snapzip` tap.

## Install From Tap

```bash
brew tap MTEnt/snapzip
brew install snapzip
```

## Development Install

```bash
brew install --HEAD ./packaging/homebrew/snapzip.rb
```

## Tap Updates

1. Publish a SnapZip tag and confirm the GitHub release completed.
2. Compute the source archive checksum:
   ```bash
   curl -L -o /tmp/snapzip-source.tar.gz https://github.com/MTEnt/SnapZip/archive/refs/tags/<tag>.tar.gz
   shasum -a 256 /tmp/snapzip-source.tar.gz
   ```
3. Update `url`, `sha256`, and release ldflags in this formula.
4. Copy the formula to `Formula/snapzip.rb` in the tap and run:
   ```bash
   brew audit --strict mtent/snapzip/snapzip
   brew reinstall --build-from-source mtent/snapzip/snapzip
   brew test mtent/snapzip/snapzip
   ```

The formula builds from source with Go and keeps SnapZip's local-first behavior: no project memory is bundled with the package. Release binaries installed through Homebrew stamp `snapzip version` with the formula version.
