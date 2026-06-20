# Homebrew Packaging

This directory contains a head-only formula scaffold for a SnapZip tap.

## Development Install

```bash
brew install --HEAD ./packaging/homebrew/snapzip.rb
```

## Tap Setup

1. Create a tap repository, such as `MTEnt/homebrew-snapzip`.
2. Copy `packaging/homebrew/snapzip.rb` to `Formula/snapzip.rb` in the tap.
3. After publishing a SnapZip tag, replace the formula `head`-only install with a stable release URL and checksum from the GitHub release `checksums.txt` asset.

Example stable source fields:

```ruby
url "https://github.com/MTEnt/SnapZip/archive/refs/tags/v0.1.0.tar.gz"
sha256 "<sha256 from checksums.txt or the source archive>"
```

The formula builds from source with Go and keeps SnapZip's local-first behavior: no project memory is bundled with the package.
