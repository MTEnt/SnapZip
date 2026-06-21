#!/usr/bin/env python3
import argparse
import re
import sys
from pathlib import Path


SKIP_DIRS = {
    ".git",
    ".pytest_cache",
    ".snapzip-ci",
    ".work",
    "__pycache__",
    "dist",
    "node_modules",
    "package",
    "vendor",
}

SKIP_SUFFIXES = {
    ".db",
    ".exe",
    ".gif",
    ".gz",
    ".ico",
    ".jpg",
    ".jpeg",
    ".pdf",
    ".png",
    ".tar",
    ".tgz",
    ".zip",
}

MAX_TEXT_FILE_BYTES = 2 * 1024 * 1024


def forbidden_patterns():
    return [
        ("local developer Documents path", re.compile(re.escape("/Users/" + "MTEnt/Documents/"))),
        ("private benchmark directory name", re.compile(re.escape("snapzip_" + "benchmark"), re.IGNORECASE)),
        ("discarded card-game test fixture", re.compile(re.escape("black" + "jack"), re.IGNORECASE)),
        ("private coding benchmark reference", re.compile(r"\b" + "S" + r"WE[-_ ]?bench(?:mark)?\b", re.IGNORECASE)),
    ]


def should_skip_path(path):
    if any(part in SKIP_DIRS for part in path.parts):
        return True
    if path.suffix.lower() in SKIP_SUFFIXES:
        return True
    return False


def looks_binary(data):
    return b"\0" in data[:2048]


def scan_file(path, patterns):
    try:
        data = path.read_bytes()
    except OSError as exc:
        return [(str(path), 0, "read error", str(exc))]

    if len(data) > MAX_TEXT_FILE_BYTES or looks_binary(data):
        return []

    text = data.decode("utf-8", errors="ignore")
    findings = []
    for line_number, line in enumerate(text.splitlines(), start=1):
        for label, pattern in patterns:
            if pattern.search(line):
                findings.append((str(path), line_number, label, line.strip()))
    return findings


def iter_files(roots):
    for root in roots:
        root = Path(root)
        if root.is_file():
            if not should_skip_path(root):
                yield root
            continue
        for path in root.rglob("*"):
            if path.is_file() and not should_skip_path(path):
                yield path


def main():
    parser = argparse.ArgumentParser(description="Fail if public files mention private benchmark fixtures or local paths.")
    parser.add_argument("--root", action="append", default=["."], help="File or directory to scan. Can be repeated.")
    args = parser.parse_args()

    patterns = forbidden_patterns()
    findings = []
    for path in iter_files(args.root):
        findings.extend(scan_file(path, patterns))

    if findings:
        print("Public safety scan failed:", file=sys.stderr)
        for path, line_number, label, line in findings:
            location = f"{path}:{line_number}" if line_number else path
            print(f"{location}: {label}: {line}", file=sys.stderr)
        return 1

    print("Public safety scan passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
