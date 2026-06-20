#!/usr/bin/env python3
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
BENCHMARK_ROOT = Path(__file__).resolve().parent
ALGORITHM_SUITE = BENCHMARK_ROOT / "suites" / "algorithm_20"
STRESS_RBT = BENCHMARK_ROOT / "stress_rbt.py"


class BenchmarkFailure(Exception):
    def __init__(self, record):
        super().__init__(f"command failed: {' '.join(record['cmd'])}")
        self.record = record


def run_cmd(cmd, cwd, allow_failure=False):
    started = time.perf_counter()
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    elapsed = time.perf_counter() - started
    record = {
        "cmd": [str(part) for part in cmd],
        "cwd": str(cwd),
        "returncode": proc.returncode,
        "elapsed_seconds": round(elapsed, 6),
        "stdout": proc.stdout,
        "stderr": proc.stderr,
    }
    if proc.returncode != 0 and not allow_failure:
        raise BenchmarkFailure(record)
    return proc, record


def resolve_snapzip_bin(value):
    candidate = value or os.environ.get("SNAPZIP_BIN")
    if not candidate:
        repo_binary = REPO_ROOT / "snapzip"
        if repo_binary.exists():
            return str(repo_binary.resolve())
        path_binary = shutil.which("snapzip")
        if path_binary:
            return path_binary
        raise SystemExit("snapzip binary not found; run `go build -o snapzip ./cmd/snapzip` or pass --snapzip-bin")

    if os.path.sep not in candidate:
        path_binary = shutil.which(candidate)
        if path_binary:
            return path_binary

    path = Path(candidate).expanduser()
    if not path.is_absolute():
        path = Path.cwd() / path
    if not path.exists():
        raise SystemExit(f"snapzip binary not found: {path}")
    return str(path.resolve())


def prepare_algorithm_work(parent, name):
    work_dir = parent / name
    shutil.copytree(ALGORITHM_SUITE, work_dir)
    _, setup_record = run_cmd([sys.executable, "setup_20_tasks.py"], work_dir)
    return work_dir, setup_record


def parse_score(output):
    match = re.search(r"Score:\s*(\d+)/(\d+)\s*\(([\d.]+)%\)", output)
    if not match:
        return {"passed": None, "total": None, "percent": None}
    return {
        "passed": int(match.group(1)),
        "total": int(match.group(2)),
        "percent": float(match.group(3)),
    }


def run_harness(work_dir):
    proc, record = run_cmd([sys.executable, "harness.py"], work_dir)
    score = parse_score(proc.stdout)
    return {**score, "command": record}


def run_algorithm_20(parent, args, snapzip_bin):
    raw_dir, raw_setup = prepare_algorithm_work(parent, "algorithm_20_raw")
    _, raw_solver = run_cmd([sys.executable, "solve_raw.py"], raw_dir)
    raw_harness = run_harness(raw_dir)

    snapzip_dir, snapzip_setup = prepare_algorithm_work(parent, "algorithm_20_snapzip")
    _, snapzip_solver = run_cmd([
        sys.executable,
        "solve_snapzip.py",
        "--snapzip-bin",
        snapzip_bin,
        "--iterations",
        str(args.iterations),
    ], snapzip_dir)
    snapzip_harness = run_harness(snapzip_dir)

    return {
        "name": "algorithm_20",
        "raw": {
            "setup": raw_setup,
            "solver": raw_solver,
            "harness": raw_harness,
        },
        "snapzip": {
            "setup": snapzip_setup,
            "solver": snapzip_solver,
            "harness": snapzip_harness,
        },
    }


def parse_json_stdout(record):
    try:
        return json.loads(record["stdout"])
    except json.JSONDecodeError as exc:
        return {
            "passed": False,
            "error": f"could not parse JSON output: {exc}",
            "stdout": record["stdout"],
            "stderr": record["stderr"],
        }


def run_rbt_candidate(parent, name, solver_cmd, allow_stress_failure):
    work_dir, setup_record = prepare_algorithm_work(parent, name)
    _, solver_record = run_cmd(solver_cmd, work_dir)
    candidate = work_dir / "tasks" / "task_7_red_black_tree.py"
    _, stress_record = run_cmd(
        [sys.executable, str(STRESS_RBT), str(candidate)],
        work_dir,
        allow_failure=allow_stress_failure,
    )
    return {
        "setup": setup_record,
        "solver": solver_record,
        "stress": {
            "result": parse_json_stdout(stress_record),
            "command": stress_record,
        },
    }


def run_hard_rbt(parent, args, snapzip_bin):
    raw = run_rbt_candidate(
        parent,
        "hard_rbt_raw",
        [sys.executable, "solve_raw.py", "--tasks", "7"],
        allow_stress_failure=True,
    )
    snapzip = run_rbt_candidate(
        parent,
        "hard_rbt_snapzip",
        [
            sys.executable,
            "solve_snapzip.py",
            "--snapzip-bin",
            snapzip_bin,
            "--iterations",
            str(args.iterations),
            "--tasks",
            "7",
        ],
        allow_stress_failure=True,
    )
    return {
        "name": "hard_rbt",
        "raw": raw,
        "snapzip": snapzip,
    }


def write_file(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def naive_file_rank(root, query):
    tokens = [token for token in re.split(r"[^A-Za-z0-9_]+", query.lower()) if len(token) > 2]
    candidates = []
    for path in root.rglob("*.py"):
        if "memory.db" in path.parts:
            continue
        text = path.read_text(encoding="utf-8", errors="ignore").lower()
        rel = path.relative_to(root).as_posix()
        score = sum(text.count(token) for token in tokens)
        score += sum(3 for token in tokens if token in rel.lower())
        if score > 0:
            candidates.append({"path": rel, "score": score})
    candidates.sort(key=lambda item: (-item["score"], item["path"]))
    return candidates


def run_repair_retrieval(parent, args, snapzip_bin):
    work_dir = parent / "repair_retrieval"
    work_dir.mkdir(parents=True, exist_ok=True)
    write_file(
        work_dir / "youtube_dl" / "utils.py",
        """
import re

def _match_one(filter_part, dct):
    UNARY_OPERATORS = {
        '': lambda v: v is not None,
        '!': lambda v: v is None,
    }
    m = re.search(r'(?P<op>!?)(?P<key>[a-z_]+)$', filter_part)
    if m:
        op = UNARY_OPERATORS[m.group('op')]
        return op(dct.get(m.group('key')))
    raise ValueError(filter_part)

def match_str(filter_str, dct):
    return all(_match_one(part, dct) for part in filter_str.split('&'))
""".strip()
        + "\n",
    )
    write_file(
        work_dir / "youtube_dl" / "extractor" / "bambuser.py",
        ("is_live archived stream metadata\n" * 120),
    )
    write_file(
        work_dir / "test" / "test_utils.py",
        """
from youtube_dl.utils import match_str

def test_match_str():
    assert not match_str('is_live', {'is_live': False})
""".strip()
        + "\n",
    )
    failure = "\n".join(
        [
            "Traceback (most recent call last):",
            f'  File "{work_dir / "test" / "test_utils.py"}", line 4, in test_match_str',
            "    assert not match_str('is_live', {'is_live': False})",
            "AssertionError: True is not false",
        ]
    )
    failure_file = work_dir / "failure.txt"
    failure_file.write_text(failure + "\n", encoding="utf-8")

    _, index_record = run_cmd(
        [snapzip_bin, "index", "--reset", "--db-dir", str(work_dir), "--crawl", str(work_dir), "--langs", "python"],
        work_dir,
    )
    raw_candidates = naive_file_rank(work_dir, failure)
    _, repair_record = run_cmd(
        [
            snapzip_bin,
            "repair-pack",
            "--db-dir",
            str(work_dir),
            "--error-file",
            str(failure_file),
            "--json",
            "--limit",
            "4",
            "--budget",
            "6000",
        ],
        work_dir,
    )
    pack = parse_json_stdout(repair_record)
    snippets = pack.get("snippets") or []
    receipts = pack.get("receipts") or []
    top = snippets[0] if snippets else {}
    passed = (
        top.get("path") == "youtube_dl/utils.py"
        and "match_str" in top.get("content", "")
        and len(receipts) > 0
    )
    return {
        "name": "repair_retrieval",
        "passed": passed,
        "raw": {
            "ranking": raw_candidates[:5],
            "top_path": raw_candidates[0]["path"] if raw_candidates else "",
        },
        "snapzip": {
            "top_path": top.get("path", ""),
            "top_location": f"{top.get('path', '')}:{top.get('start_line', '')}-{top.get('end_line', '')}",
            "receipt_count": len(receipts),
            "pack": pack,
            "index": index_record,
            "repair_pack": repair_record,
        },
    }


def snapzip_passed(result):
    if result["name"] == "algorithm_20":
        harness = result["snapzip"]["harness"]
        return harness["passed"] == harness["total"]
    if result["name"] == "hard_rbt":
        return bool(result["snapzip"]["stress"]["result"].get("passed"))
    if result["name"] == "repair_retrieval":
        return bool(result.get("passed"))
    return False


def main():
    parser = argparse.ArgumentParser(description="Run reproducible SnapZip benchmark comparisons.")
    parser.add_argument("--suite", choices=["smoke", "algorithm-20", "hard-rbt", "repair-retrieval", "all"], default="smoke")
    parser.add_argument("--snapzip-bin", default="", help="Path to a built snapzip binary")
    parser.add_argument("--iterations", type=int, default=100)
    parser.add_argument("--json", default="", help="Optional path to write the JSON report")
    parser.add_argument("--keep-workdir", default="", help="Optional directory to keep generated benchmark files")
    args = parser.parse_args()

    snapzip_bin = resolve_snapzip_bin(args.snapzip_bin)
    started = time.perf_counter()
    result = {
        "suite": args.suite,
        "snapzip_bin": snapzip_bin,
        "iterations": args.iterations,
        "runs": [],
    }

    temp_context = None
    try:
        if args.keep_workdir:
            work_parent = Path(args.keep_workdir).expanduser().resolve()
            work_parent.mkdir(parents=True, exist_ok=True)
        else:
            temp_context = tempfile.TemporaryDirectory(prefix="snapzip-bench-")
            work_parent = Path(temp_context.name)

        if args.suite in ("smoke", "hard-rbt", "all"):
            result["runs"].append(run_hard_rbt(work_parent, args, snapzip_bin))
        if args.suite in ("repair-retrieval", "all"):
            result["runs"].append(run_repair_retrieval(work_parent, args, snapzip_bin))
        if args.suite in ("algorithm-20", "all"):
            result["runs"].append(run_algorithm_20(work_parent, args, snapzip_bin))

        result["elapsed_seconds"] = round(time.perf_counter() - started, 6)
        result["passed"] = all(snapzip_passed(run) for run in result["runs"])
        if args.keep_workdir:
            result["workdir"] = str(work_parent)
    except BenchmarkFailure as exc:
        result["elapsed_seconds"] = round(time.perf_counter() - started, 6)
        result["passed"] = False
        result["error"] = exc.record
    finally:
        if temp_context is not None:
            temp_context.cleanup()

    output = json.dumps(result, indent=2)
    print(output)
    if args.json:
        output_path = Path(args.json).expanduser()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(output + "\n", encoding="utf-8")

    return 0 if result.get("passed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
