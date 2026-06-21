#!/usr/bin/env python3
import argparse
import gzip
import hashlib
import json
import math
import os
import pickle
import random
import re
import shlex
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
REPOBENCH_R_CONFIGS = ("python_cff", "python_cfr", "java_cff", "java_cfr")
REPOBENCH_R_SPLITS = ("easy", "hard")


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


def tokenize_for_retrieval(text):
    return [token for token in re.split(r"[^A-Za-z0-9]+", text.lower()) if len(token) > 1]


def top_k_indices(scores, k):
    return [idx for idx, _ in sorted(enumerate(scores), key=lambda item: (-item[1], item[0]))[:k]]


def gold_rank(gold, top):
    try:
        return top.index(gold) + 1
    except ValueError:
        return 0


def reciprocal_rank(gold, top, k):
    rank = gold_rank(gold, top[:k])
    if rank == 0:
        return 0.0
    return 1.0 / rank


def ndcg_at_k(gold, top, k):
    rank = gold_rank(gold, top[:k])
    if rank == 0:
        return 0.0
    return 1.0 / math.log2(rank + 1)


def duplicate_result_count(top):
    valid = [item for item in top if item >= 0]
    return len(valid) - len(set(valid))


def safe_mean(values):
    if not values:
        return 0.0
    return round(sum(values) / len(values), 6)


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


def receipts_contain(receipts, field, fragment):
    fragment = fragment.lower()
    for receipt in receipts or []:
        for value in receipt.get(field, []) or []:
            if fragment in value.lower():
                return True
    return False


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


def run_context_quality(parent, args, snapzip_bin):
    work_dir = parent / "context_quality"
    work_dir.mkdir(parents=True, exist_ok=True)
    write_file(
        work_dir / "app" / "cache.py",
        """
class CacheStore:
    def __init__(self):
        self.values = {}

    def put(self, key, value):
        self.values[key] = value

    def get(self, key):
        return self.values.get(key)


def build_cache(seed):
    cache = CacheStore()
    cache.put('seed', seed)
    return cache
""".strip()
        + "\n",
    )
    write_file(
        work_dir / "tests" / "test_cache.py",
        """
from app.cache import build_cache


def test_build_cache_returns_seed():
    cache = build_cache('ready')
    assert cache.get('seed') == 'ready'
""".strip()
        + "\n",
    )
    write_file(
        work_dir / "notes" / "cache_noise.py",
        ("cache cache cache archive metadata seed ready\n" * 90),
    )
    query = "CacheStore build_cache seed test"
    _, index_record = run_cmd(
        [snapzip_bin, "index", "--reset", "--db-dir", str(work_dir), "--crawl", str(work_dir), "--langs", "python"],
        work_dir,
    )
    raw_candidates = naive_file_rank(work_dir, query)
    _, pack_record = run_cmd(
        [
            snapzip_bin,
            "pack",
            "--db-dir",
            str(work_dir),
            "--query",
            query,
            "--mode",
            "test",
            "--json",
            "--limit",
            "4",
            "--budget",
            "8000",
        ],
        work_dir,
    )
    _, source_graph_record = run_cmd(
        [
            snapzip_bin,
            "graph",
            "--db-dir",
            str(work_dir),
            "--path",
            "app/cache.py",
            "--json",
            "--limit",
            "10",
        ],
        work_dir,
    )
    _, test_graph_record = run_cmd(
        [
            snapzip_bin,
            "graph",
            "--db-dir",
            str(work_dir),
            "--path",
            "tests/test_cache.py",
            "--json",
            "--limit",
            "10",
        ],
        work_dir,
    )

    rerank_dir = parent / "structural_rerank"
    rerank_dir.mkdir(parents=True, exist_ok=True)
    write_file(
        rerank_dir / "app" / "payment_gateway.py",
        """
class PaymentGateway:
    def authorize_payment(self, amount):
        return amount
""".strip()
        + "\n",
    )
    write_file(
        rerank_dir / "app" / "checkout.py",
        """
from app.payment_gateway import PaymentGateway


def checkout(amount):
    return PaymentGateway().authorize_payment(amount)
""".strip()
        + "\n",
    )
    write_file(
        rerank_dir / "notes" / "payment_noise.py",
        ("payment gateway authorize payment amount checkout workflow\n" * 80),
    )
    rerank_query = "PaymentGateway authorize_payment checkout"
    rerank_raw_candidates = naive_file_rank(rerank_dir, rerank_query)
    _, rerank_index_record = run_cmd(
        [snapzip_bin, "index", "--reset", "--db-dir", str(rerank_dir), "--crawl", str(rerank_dir), "--langs", "python"],
        rerank_dir,
    )
    _, rerank_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(rerank_dir),
            "--query",
            rerank_query,
            "--json",
            "--limit",
            "3",
        ],
        rerank_dir,
    )

    ast_chunk_dir = parent / "ast_chunking"
    ast_chunk_dir.mkdir(parents=True, exist_ok=True)
    go_registry = (
        "var HugeRegistry = map[string]string{\n"
        + "\t\"feature\": \"enabled\",\n" * 18
        + "}\n\n"
    )
    go_alpha = "func Alpha() string {\n\treturn \"alpha\"\n}\n"
    go_beta = (
        "func Beta() string {\n"
        + "\tvalue := \"beta\"\n" * 5
        + "\treturn value\n}\n"
    )
    write_file(
        ast_chunk_dir / "pkg" / "registry.go",
        "package pkg\n\n" + go_registry + go_alpha + "\n" + go_beta,
    )
    ast_chunk_query = "Alpha alpha"
    ast_chunk_max_bytes = len(("\n" + go_alpha).encode("utf-8")) + 2
    _, ast_chunk_index_record = run_cmd(
        [
            snapzip_bin,
            "index",
            "--reset",
            "--db-dir",
            str(ast_chunk_dir),
            "--crawl",
            str(ast_chunk_dir),
            "--langs",
            "go",
            "--max-content-bytes",
            str(ast_chunk_max_bytes),
        ],
        ast_chunk_dir,
    )
    _, ast_chunk_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(ast_chunk_dir),
            "--query",
            ast_chunk_query,
            "--json",
            "--limit",
            "5",
        ],
        ast_chunk_dir,
    )

    py_chunk_dir = parent / "python_structural_chunking"
    py_chunk_dir.mkdir(parents=True, exist_ok=True)
    py_routes = (
        "ROUTES = {\n"
        + "    \"feature\": \"enabled\",\n" * 18
        + "}\n\n"
    )
    py_alpha = "def alpha():\n    return \"alpha\"\n"
    py_beta = (
        "def beta():\n"
        + "    value = \"beta\"\n" * 5
        + "    return value\n"
    )
    write_file(py_chunk_dir / "app" / "handlers.py", py_routes + py_alpha + "\n" + py_beta)
    py_chunk_query = "alpha return alpha"
    py_chunk_max_bytes = len(("\n" + py_alpha).encode("utf-8")) + 2
    _, py_chunk_index_record = run_cmd(
        [
            snapzip_bin,
            "index",
            "--reset",
            "--db-dir",
            str(py_chunk_dir),
            "--crawl",
            str(py_chunk_dir),
            "--langs",
            "python",
            "--max-content-bytes",
            str(py_chunk_max_bytes),
        ],
        py_chunk_dir,
    )
    _, py_chunk_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(py_chunk_dir),
            "--query",
            py_chunk_query,
            "--json",
            "--limit",
            "5",
        ],
        py_chunk_dir,
    )

    popular_chunk_dir = parent / "popular_structural_chunking"
    popular_chunk_dir.mkdir(parents=True, exist_ok=True)
    js_registry = (
        "const registry = {\n"
        + "  feature: \"enabled\",\n" * 18
        + "};\n\n"
    )
    js_alpha = "export function alpha() {\n  return \"alpha\"\n}\n"
    js_beta = (
        "export function beta() {\n"
        + "  const value = \"beta\"\n" * 5
        + "  return value\n}\n"
    )
    ruby_alpha = "class Alpha\n  def value\n    \"alpha\"\n  end\nend\n"
    ruby_beta = (
        "class Beta\n"
        + "  def value\n    \"beta\"\n  end\n" * 5
        + "end\n"
    )
    write_file(popular_chunk_dir / "web" / "actions.js", js_registry + js_alpha + "\n" + js_beta)
    write_file(popular_chunk_dir / "lib" / "workers.rb", ruby_alpha + "\n" + ruby_beta)
    popular_chunk_max_bytes = 96
    _, popular_chunk_index_record = run_cmd(
        [
            snapzip_bin,
            "index",
            "--reset",
            "--db-dir",
            str(popular_chunk_dir),
            "--crawl",
            str(popular_chunk_dir),
            "--langs",
            "javascript,ruby",
            "--max-content-bytes",
            str(popular_chunk_max_bytes),
        ],
        popular_chunk_dir,
    )
    _, popular_js_chunk_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(popular_chunk_dir),
            "--query",
            "function alpha return alpha javascript",
            "--json",
            "--limit",
            "10",
        ],
        popular_chunk_dir,
    )
    _, popular_ruby_chunk_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(popular_chunk_dir),
            "--query",
            "class Alpha value alpha ruby",
            "--json",
            "--limit",
            "10",
        ],
        popular_chunk_dir,
    )

    multipath_dir = parent / "multipath_query"
    multipath_dir.mkdir(parents=True, exist_ok=True)
    for idx in range(80):
        write_file(
            multipath_dir / "noise" / f"exact_{idx:03d}.py",
            "refreshtoken getorcreate retry failure\n",
        )
    write_file(
        multipath_dir / "app" / "session_cache.py",
        """
class SessionCache:
    def get_or_create(self, refresh_token):
        \"\"\"get create refresh token session cache\"\"\"
        return refresh_token
""".strip()
        + "\n",
    )
    _, multipath_index_record = run_cmd(
        [
            snapzip_bin,
            "index",
            "--reset",
            "--db-dir",
            str(multipath_dir),
            "--crawl",
            str(multipath_dir),
            "--langs",
            "python",
        ],
        multipath_dir,
    )
    _, multipath_search_record = run_cmd(
        [
            snapzip_bin,
            "search",
            "--db-dir",
            str(multipath_dir),
            "--query",
            "fix getOrCreate refreshToken",
            "--json",
            "--limit",
            "5",
        ],
        multipath_dir,
    )
    pack = parse_json_stdout(pack_record)
    source_graph = parse_json_stdout(source_graph_record)
    test_graph = parse_json_stdout(test_graph_record)
    rerank_payload = parse_json_stdout(rerank_record)
    rerank_paths = [snippet.get("path", "") for snippet in rerank_payload.get("snippets", [])]
    ast_chunk_payload = parse_json_stdout(ast_chunk_record)
    ast_alpha_snippets = [
        snippet
        for snippet in ast_chunk_payload.get("snippets", [])
        if "func Alpha" in snippet.get("content", "")
    ]
    py_chunk_payload = parse_json_stdout(py_chunk_record)
    py_alpha_snippets = [
        snippet
        for snippet in py_chunk_payload.get("snippets", [])
        if "def alpha" in snippet.get("content", "")
    ]
    popular_js_chunk_payload = parse_json_stdout(popular_js_chunk_record)
    popular_js_alpha_snippets = [
        snippet
        for snippet in popular_js_chunk_payload.get("snippets", [])
        if "function alpha" in snippet.get("content", "")
    ]
    popular_ruby_chunk_payload = parse_json_stdout(popular_ruby_chunk_record)
    popular_ruby_alpha_snippets = [
        snippet
        for snippet in popular_ruby_chunk_payload.get("snippets", [])
        if "class Alpha" in snippet.get("content", "")
    ]
    multipath_payload = parse_json_stdout(multipath_search_record)
    multipath_paths = [snippet.get("path", "") for snippet in multipath_payload.get("snippets", [])]
    multipath_receipts = multipath_payload.get("receipts") or []
    receipts = pack.get("receipts") or []
    quality = pack.get("quality") or {}
    metrics = quality.get("metrics") or {}
    has_symbol_graph_reason = receipts_contain(receipts, "reasons", "local symbol reference graph")
    has_symbol_graph_evidence = (
        receipts_contain(receipts, "evidence", "references build_cache")
        and receipts_contain(receipts, "evidence", "references get")
    )
    source_graph_symbol_edges_passed = (
        any(symbol.get("name") == "build_cache" for symbol in source_graph.get("symbols") or [])
        and any(ref.get("path") == "tests/test_cache.py" for ref in source_graph.get("referenced_by") or [])
    )
    test_graph_definition_edges_passed = any(
        symbol.get("path") == "app/cache.py" and symbol.get("name") in {"build_cache", "get"}
        for symbol in test_graph.get("reference_definitions") or []
    )
    structural_rerank_passed = (
        len(rerank_paths) >= 2
        and rerank_paths[0] == "app/payment_gateway.py"
        and "app/checkout.py" in rerank_paths[:3]
    )
    ast_chunking_passed = bool(ast_alpha_snippets) and all(
        "HugeRegistry" not in snippet.get("content", "")
        and "func Beta" not in snippet.get("content", "")
        and "return \"alpha\"" in snippet.get("content", "")
        for snippet in ast_alpha_snippets
    )
    python_structural_chunking_passed = bool(py_alpha_snippets) and all(
        "ROUTES" not in snippet.get("content", "")
        and "def beta" not in snippet.get("content", "")
        and "return \"alpha\"" in snippet.get("content", "")
        for snippet in py_alpha_snippets
    )
    popular_structural_chunking_passed = (
        bool(popular_js_alpha_snippets)
        and bool(popular_ruby_alpha_snippets)
        and all(
            "registry" not in snippet.get("content", "")
            and "function beta" not in snippet.get("content", "")
            and "return \"alpha\"" in snippet.get("content", "")
            for snippet in popular_js_alpha_snippets
        )
        and all(
            "class Beta" not in snippet.get("content", "")
            and "\"alpha\"" in snippet.get("content", "")
            for snippet in popular_ruby_alpha_snippets
        )
    )
    multipath_query_passed = (
        "app/session_cache.py" in multipath_paths[:5]
        and receipts_contain(multipath_receipts, "reasons", "expanded identifier retrieval path")
        and receipts_contain(multipath_receipts, "evidence", "get, create, refresh, token")
    )
    passed = (
        quality.get("score", 0) >= 0.55
        and metrics.get("snippet_count", 0) > 0
        and metrics.get("receipt_coverage", 0) > 0
        and metrics.get("definition_count", 0) > 0
        and metrics.get("reference_count", 0) > 0
        and metrics.get("test_snippet_count", 0) > 0
        and metrics.get("graph_receipt_count", 0) > 0
        and metrics.get("graph_evidence_count", 0) > 0
        and has_symbol_graph_reason
        and has_symbol_graph_evidence
        and source_graph_symbol_edges_passed
        and test_graph_definition_edges_passed
        and structural_rerank_passed
        and ast_chunking_passed
        and python_structural_chunking_passed
        and popular_structural_chunking_passed
        and multipath_query_passed
    )
    return {
        "name": "context_quality",
        "passed": passed,
        "raw": {
            "ranking": raw_candidates[:5],
            "top_path": raw_candidates[0]["path"] if raw_candidates else "",
            "has_quality_metrics": False,
            "structural_rerank_ranking": rerank_raw_candidates[:5],
            "ast_chunking_has_structural_chunks": False,
            "python_structural_chunking_has_structural_chunks": False,
            "popular_structural_chunking_has_structural_chunks": False,
            "multipath_query_ranking_has_expanded_identifier_path": False,
        },
        "snapzip": {
            "quality": quality,
            "paths": [snippet.get("path", "") for snippet in pack.get("snippets") or []],
            "receipt_count": len(receipts),
            "has_symbol_graph_reason": has_symbol_graph_reason,
            "has_symbol_graph_evidence": has_symbol_graph_evidence,
            "source_graph_symbol_edges_passed": source_graph_symbol_edges_passed,
            "test_graph_definition_edges_passed": test_graph_definition_edges_passed,
            "structural_rerank_passed": structural_rerank_passed,
            "structural_rerank_paths": rerank_paths,
            "ast_chunking_passed": ast_chunking_passed,
            "ast_chunking_alpha_locations": [
                f"{snippet.get('path', '')}:{snippet.get('start_line', '')}-{snippet.get('end_line', '')}"
                for snippet in ast_alpha_snippets
            ],
            "python_structural_chunking_passed": python_structural_chunking_passed,
            "python_structural_chunking_alpha_locations": [
                f"{snippet.get('path', '')}:{snippet.get('start_line', '')}-{snippet.get('end_line', '')}"
                for snippet in py_alpha_snippets
            ],
            "popular_structural_chunking_passed": popular_structural_chunking_passed,
            "popular_js_structural_chunking_alpha_locations": [
                f"{snippet.get('path', '')}:{snippet.get('start_line', '')}-{snippet.get('end_line', '')}"
                for snippet in popular_js_alpha_snippets
            ],
            "popular_ruby_structural_chunking_alpha_locations": [
                f"{snippet.get('path', '')}:{snippet.get('start_line', '')}-{snippet.get('end_line', '')}"
                for snippet in popular_ruby_alpha_snippets
            ],
            "multipath_query_passed": multipath_query_passed,
            "multipath_query_paths": multipath_paths,
            "multipath_query_receipt_count": len(multipath_receipts),
            "pack": pack,
            "source_graph": source_graph,
            "test_graph": test_graph,
            "index": index_record,
            "context_pack": pack_record,
            "source_graph_record": source_graph_record,
            "test_graph_record": test_graph_record,
            "structural_rerank_index": rerank_index_record,
            "structural_rerank_search": rerank_record,
            "ast_chunking_index": ast_chunk_index_record,
            "ast_chunking_search": ast_chunk_record,
            "python_structural_chunking_index": py_chunk_index_record,
            "python_structural_chunking_search": py_chunk_record,
            "popular_structural_chunking_index": popular_chunk_index_record,
            "popular_js_structural_chunking_search": popular_js_chunk_record,
            "popular_ruby_structural_chunking_search": popular_ruby_chunk_record,
            "multipath_query_index": multipath_index_record,
            "multipath_query_search": multipath_search_record,
        },
    }


def comma_list(value, defaults):
    items = [item.strip() for item in (value or "").split(",") if item.strip()]
    return items or list(defaults)


def safe_dir_name(value):
    cleaned = re.sub(r"[^A-Za-z0-9._-]+", "_", value)
    cleaned = cleaned.strip("._-")
    return cleaned or "run"


def resolve_repobench_data_path(data_root, config):
    path = Path(data_root).expanduser()
    if path.is_file():
        return path
    if path.is_dir():
        candidates = [
            path / f"{config}.gz",
            path / "data" / f"{config}.gz",
        ]
        for candidate in candidates:
            if candidate.exists():
                return candidate
        raise SystemExit(f"RepoBench-R data file not found for {config} under: {path}")
    return path


def resolve_repobench_data(args, config=None):
    config = config or args.repobench_config
    if args.repobench_data:
        path = resolve_repobench_data_path(args.repobench_data, config)
    elif os.environ.get("REPOBENCH_R_DATA"):
        path = resolve_repobench_data_path(os.environ["REPOBENCH_R_DATA"], config)
    else:
        try:
            from huggingface_hub import snapshot_download
        except ImportError as exc:
            raise SystemExit(
                "RepoBench-R data not found. Install huggingface_hub or pass --repobench-data "
                "pointing at a data/<config>.gz file or data directory from tianyang/repobench-r."
            ) from exc
        snapshot = Path(
            snapshot_download(
                repo_id="tianyang/repobench-r",
                repo_type="dataset",
                allow_patterns=["README.md", "repobench-r.py", f"data/{config}.gz"],
            )
        )
        path = snapshot / "data" / f"{config}.gz"

    if not path.exists():
        raise SystemExit(f"RepoBench-R data file not found: {path}")
    return path.resolve()


def load_repobench_rows(path, split):
    with gzip.open(path, "rb") as handle:
        payload = pickle.load(handle)
    try:
        return payload["test"][split]
    except KeyError as exc:
        raise SystemExit(f"RepoBench-R split not found in {path}: test/{split}") from exc


def repobench_r_language(config):
    if config.startswith("python_"):
        return "python"
    if config.startswith("java_"):
        return "java"
    raise SystemExit(f"unsupported RepoBench-R config: {config}")


def repobench_r_extension(language):
    if language == "java":
        return ".java"
    if language == "python":
        return ".py"
    raise SystemExit(f"unsupported RepoBench-R language: {language}")


def repobench_sample_indices(row_count, sample_size, seed):
    if row_count <= 0:
        return []
    if sample_size <= 0 or sample_size >= row_count:
        return list(range(row_count))
    return sorted(random.Random(seed).sample(range(row_count), sample_size))


def repobench_query(row):
    return "\n".join(row["code"].splitlines()[-3:])


def repobench_p_split_name(value):
    aliases = {
        "cff": "cross_file_first",
        "cross-file-first": "cross_file_first",
        "cross_file_first": "cross_file_first",
        "cfr": "cross_file_random",
        "cross-file-random": "cross_file_random",
        "cross_file_random": "cross_file_random",
        "if": "in_file",
        "in-file": "in_file",
        "in_file": "in_file",
    }
    return aliases.get(value, value)


def repobench_p_repo_id(language):
    language = language.lower()
    if language not in {"python", "java"}:
        raise SystemExit(f"unsupported RepoBench v1.1 language: {language}")
    return f"tianyang/repobench_{language}_v1.1"


def repobench_p_paths_from_arg(value, split):
    if not value:
        return []
    path = Path(value).expanduser()
    if path.is_dir():
        return sorted(path.glob(f"{split}-*.parquet"))
    return [path]


def resolve_repobench_p_paths(args):
    split = repobench_p_split_name(args.repobench_p_split)
    explicit = repobench_p_paths_from_arg(args.repobench_p_data, split)
    if explicit:
        missing = [str(path) for path in explicit if not path.exists()]
        if missing:
            raise SystemExit("RepoBench v1.1 parquet file not found: " + ", ".join(missing))
        return explicit, "local"

    try:
        from huggingface_hub import hf_hub_download, list_repo_files
    except ImportError as exc:
        raise SystemExit(
            "RepoBench v1.1 data not found. Install huggingface_hub or pass --repobench-p-data "
            "pointing at downloaded parquet files."
        ) from exc

    repo_id = repobench_p_repo_id(args.repobench_p_language)
    files = sorted(
        name
        for name in list_repo_files(repo_id, repo_type="dataset")
        if name.startswith(f"data/{split}-") and name.endswith(".parquet")
    )
    if not files:
        raise SystemExit(f"RepoBench v1.1 split not found: {repo_id}/{split}")
    if args.repobench_p_max_shards > 0:
        files = files[:args.repobench_p_max_shards]
    paths = [Path(hf_hub_download(repo_id, name, repo_type="dataset")) for name in files]
    return paths, repo_id


def load_repobench_p_rows(args):
    try:
        import pyarrow.parquet as pq
    except ImportError as exc:
        raise SystemExit(
            "RepoBench v1.1 parquet loading requires pyarrow. Install pyarrow or pass a preprocessed JSON benchmark later."
        ) from exc

    columns = [
        "repo_name",
        "file_path",
        "context",
        "import_statement",
        "cropped_code",
        "next_line",
        "gold_snippet_index",
    ]
    paths, source = resolve_repobench_p_paths(args)
    rows = []
    for path in paths:
        table = pq.read_table(path, columns=columns)
        rows.extend(table.to_pylist())
    if not rows:
        raise SystemExit("RepoBench v1.1 split contained no rows")
    return rows, source, [str(path) for path in paths]


def repobench_p_query(row):
    tail = "\n".join((row.get("cropped_code") or "").splitlines()[-3:])
    imports = row.get("import_statement") or ""
    return (imports + "\n" + tail).strip()


def repobench_p_raw_prompt(row):
    return "\n".join(
        part
        for part in [row.get("import_statement") or "", row.get("cropped_code") or ""]
        if part.strip()
    )


def repobench_p_candidate_text(candidate, language):
    comment = "#" if language == "python" else "//"
    path = candidate.get("path") or ""
    identifier = candidate.get("identifier") or ""
    snippet = (candidate.get("snippet") or "").rstrip()
    return f"{comment} Path: {path}\n{comment} Identifier: {identifier}\n{snippet}\n"


def repobench_p_context_texts(row, language):
    return [repobench_p_candidate_text(candidate, language) for candidate in row.get("context") or []]


def safe_relative_path_parts(value):
    parts = []
    for part in Path(value or "").parts:
        if part in ("", ".", "..", "/", "\\") or part.endswith(":"):
            continue
        clean = re.sub(r"[^A-Za-z0-9._-]+", "_", part)
        clean = clean.strip("._")
        if clean:
            parts.append(clean)
    return parts


def report_path(value):
    path = Path(value or "")
    try:
        return str(path.resolve().relative_to(REPO_ROOT))
    except (OSError, ValueError):
        pass
    return path.name or str(value)


def report_paths(values):
    return [report_path(value) for value in values]


def completion_support_tokens(text):
    stop = {
        "and", "as", "async", "await", "break", "case", "catch", "class", "const", "continue",
        "def", "else", "elif", "enum", "except", "false", "finally", "for", "from", "func",
        "function", "if", "import", "in", "interface", "is", "let", "new", "none", "not",
        "null", "or", "package", "pass", "public", "private", "protected", "return", "self",
        "static", "switch", "this", "throw", "true", "try", "var", "void", "while", "with",
    }
    return [token for token in tokenize_for_retrieval(text) if token not in stop and not token.isdigit()]


def selected_context_text(indices, candidate_texts):
    return "\n".join(candidate_texts[idx] for idx in indices if 0 <= idx < len(candidate_texts))


def token_coverage(target_tokens, context_text):
    target = set(target_tokens)
    if not target:
        return 0.0
    available = set(completion_support_tokens(context_text))
    return len(target & available) / len(target)


def token_f1(predicted, expected):
    predicted_tokens = completion_support_tokens(predicted)
    expected_tokens = completion_support_tokens(expected)
    if not predicted_tokens and not expected_tokens:
        return 1.0
    if not predicted_tokens or not expected_tokens:
        return 0.0

    predicted_counts = {}
    for token in predicted_tokens:
        predicted_counts[token] = predicted_counts.get(token, 0) + 1
    expected_counts = {}
    for token in expected_tokens:
        expected_counts[token] = expected_counts.get(token, 0) + 1

    overlap = 0
    for token, count in predicted_counts.items():
        overlap += min(count, expected_counts.get(token, 0))
    if overlap == 0:
        return 0.0
    precision = overlap / len(predicted_tokens)
    recall = overlap / len(expected_tokens)
    return 2 * precision * recall / (precision + recall)


def deterministic_random_top5(candidate_count, seed):
    if candidate_count <= 0:
        return []
    indices = list(range(candidate_count))
    rng = random.Random(seed)
    rng.shuffle(indices)
    return indices[:5]


def jaccard_top5(query, candidates):
    query_tokens = set(tokenize_for_retrieval(query))
    scores = []
    for candidate in candidates:
        candidate_tokens = set(tokenize_for_retrieval(candidate))
        union = query_tokens | candidate_tokens
        if not union:
            scores.append(0.0)
            continue
        scores.append(len(query_tokens & candidate_tokens) / len(union))
    return top_k_indices(scores, 5)


def bm25_top5(query, candidates):
    query_tokens = tokenize_for_retrieval(query)
    documents = [tokenize_for_retrieval(candidate) for candidate in candidates]
    if not documents:
        return []

    doc_count = len(documents)
    avg_len = sum(len(document) for document in documents) / doc_count
    doc_freq = {}
    for document in documents:
        for token in set(document):
            doc_freq[token] = doc_freq.get(token, 0) + 1

    k1 = 1.2
    b = 0.75
    scores = []
    for document in documents:
        counts = {}
        for token in document:
            counts[token] = counts.get(token, 0) + 1
        doc_len = len(document) or 1
        score = 0.0
        for token in query_tokens:
            freq = counts.get(token, 0)
            if freq == 0:
                continue
            df = doc_freq.get(token, 0)
            idf = math.log(1 + (doc_count - df + 0.5) / (df + 0.5))
            denom = freq + k1 * (1 - b + b * doc_len / avg_len)
            score += idf * freq * (k1 + 1) / denom
        scores.append(score)
    return top_k_indices(scores, 5)


def snapzip_top5_from_search(output):
    payload = json.loads(output)
    if isinstance(payload, dict):
        snippets = payload.get("snippets") or payload.get("results") or []
        receipt_count = len(payload.get("receipts") or [])
    else:
        snippets = payload
        receipt_count = 0

    top5 = []
    for snippet in snippets[:5]:
        path = snippet.get("path") or ""
        try:
            top5.append(int(Path(path).stem.split("_")[-1]))
        except (ValueError, IndexError):
            top5.append(-1)
    return top5, len(snippets), receipt_count


def snapzip_diagnostics_from_search(output, limit=5):
    payload = json.loads(output)
    if not isinstance(payload, dict):
        return []
    diagnostics = []
    for rank, snippet in enumerate((payload.get("snippets") or payload.get("results") or [])[:limit], start=1):
        item = {
            "rank": rank,
            "path": snippet.get("path") or "",
            "score": snippet.get("score"),
        }
        if snippet.get("diagnostics"):
            item["diagnostics"] = snippet["diagnostics"]
        diagnostics.append(item)
    return diagnostics


def snapzip_eval_search_command(snapzip_bin, db_dir, query, limit, diagnostics=False, rerank_cmd=""):
    cmd = [
        snapzip_bin,
        "search",
        "--json",
        "--limit",
        str(limit),
        "--db-dir",
        str(db_dir),
    ]
    if rerank_cmd:
        cmd.extend(["--rerank-cmd", rerank_cmd])
    if diagnostics:
        cmd.append("--diagnostics")
    cmd.extend(["--query", query])
    return cmd


def requested_snapzip_diagnostics_limit(args):
    if not args.snapzip_diagnostics:
        return 0
    return args.snapzip_diagnostics_limit or args.snapzip_search_limit


def run_repobench_r(parent, args, snapzip_bin, config=None, split=None, name=None):
    config = config or args.repobench_config
    split = split or args.repobench_split
    language = repobench_r_language(config)
    ext = repobench_r_extension(language)
    data_path = resolve_repobench_data(args, config)
    rows = load_repobench_rows(data_path, split)
    sample_indices = repobench_sample_indices(len(rows), args.repobench_sample_size, args.repobench_seed)
    sample_size = len(sample_indices)
    if sample_size <= 0:
        raise SystemExit(f"RepoBench-R split has no rows: {config} test/{split}")
    run_name = name or "repobench_r"
    work_dir = parent / safe_dir_name(run_name)
    if work_dir.exists():
        shutil.rmtree(work_dir)
    work_dir.mkdir(parents=True, exist_ok=True)

    records = []
    index_times = []
    search_times = []
    diagnostics_search_times = []
    started = time.perf_counter()
    for case_no, row_idx in enumerate(sample_indices):
        row = rows[row_idx]
        case_dir = work_dir / f"case_{case_no:03d}_row_{row_idx:05d}"
        source_dir = case_dir / "snippets"
        db_dir = case_dir / "db"
        for snippet_idx, snippet in enumerate(row["context"]):
            write_file(source_dir / f"snippet_{snippet_idx:03d}{ext}", snippet.rstrip() + "\n")

        _, index_record = run_cmd(
            [
                snapzip_bin,
                "index",
                "--reset",
                "--db-dir",
                str(db_dir),
                "--crawl",
                str(source_dir),
                "--langs",
                language,
            ],
            REPO_ROOT,
        )
        query = repobench_query(row)
        diagnostics_limit = requested_snapzip_diagnostics_limit(args)
        main_search_includes_diagnostics = args.snapzip_diagnostics and diagnostics_limit <= args.snapzip_search_limit
        search_cmd = snapzip_eval_search_command(
            snapzip_bin,
            db_dir,
            query,
            args.snapzip_search_limit,
            diagnostics=main_search_includes_diagnostics,
        )
        _, search_record = run_cmd(search_cmd, REPO_ROOT)
        diagnostics_record = search_record
        if args.snapzip_diagnostics and diagnostics_limit > args.snapzip_search_limit:
            diagnostics_cmd = snapzip_eval_search_command(
                snapzip_bin,
                db_dir,
                query,
                diagnostics_limit,
                diagnostics=True,
            )
            _, diagnostics_record = run_cmd(diagnostics_cmd, REPO_ROOT)

        jaccard_top = jaccard_top5(query, row["context"])
        bm25_top = bm25_top5(query, row["context"])
        snapzip_top, snapzip_return_count, snapzip_receipt_count = snapzip_top5_from_search(search_record["stdout"])
        _, snapzip_diagnostics_return_count, snapzip_diagnostics_receipt_count = snapzip_top5_from_search(
            diagnostics_record["stdout"]
        )
        gold = row["golden_snippet_index"]
        record = {
            "case": case_no,
            "dataset_row_index": row_idx,
            "repo_name": row["repo_name"],
            "file_path": row["file_path"],
            "candidate_count": len(row["context"]),
            "query_last_3_lines": query,
            "gold_snippet_index": gold,
            "jaccard_top5": jaccard_top,
            "bm25_top5": bm25_top,
            "snapzip_top5": snapzip_top,
            "snapzip_return_count": snapzip_return_count,
            "snapzip_receipt_count": snapzip_receipt_count,
            "index_elapsed_seconds": index_record["elapsed_seconds"],
            "search_elapsed_seconds": search_record["elapsed_seconds"],
        }
        for name in ("jaccard", "bm25", "snapzip"):
            top = record[f"{name}_top5"]
            record[f"{name}_gold_rank"] = gold_rank(gold, top)
            record[f"{name}_rr@5"] = round(reciprocal_rank(gold, top, 5), 6)
            record[f"{name}_ndcg@5"] = round(ndcg_at_k(gold, top, 5), 6)
            record[f"{name}_duplicate_count@5"] = duplicate_result_count(top[:5])
            for k in (1, 3, 5):
                record[f"{name}_hit@{k}"] = gold in top[:k]
        if args.snapzip_diagnostics:
            record["snapzip_diagnostics_limit"] = diagnostics_limit
            record["snapzip_diagnostics_return_count"] = snapzip_diagnostics_return_count
            record["snapzip_diagnostics_receipt_count"] = snapzip_diagnostics_receipt_count
            record["snapzip_diagnostics_elapsed_seconds"] = diagnostics_record["elapsed_seconds"]
            record["snapzip_diagnostics"] = snapzip_diagnostics_from_search(diagnostics_record["stdout"], diagnostics_limit)
            diagnostics_search_times.append(diagnostics_record["elapsed_seconds"])
        records.append(record)
        index_times.append(index_record["elapsed_seconds"])
        search_times.append(search_record["elapsed_seconds"])

    result = {
        "name": run_name,
        "dataset": "tianyang/repobench-r",
        "config": config,
        "split": split,
        "language": language,
        "sample_size": sample_size,
        "sample_size_arg": args.repobench_sample_size,
        "sample_mode": "full" if args.repobench_sample_size <= 0 or sample_size == len(rows) else "sampled",
        "sample_seed": args.repobench_seed,
        "sample_indices": sample_indices,
        "query": "last 3 lines of in-file code before target line",
        "snapzip_search_limit": args.snapzip_search_limit,
        "snapzip_diagnostics_limit": requested_snapzip_diagnostics_limit(args),
        "snapzip_command": f"snapzip search --json --limit {args.snapzip_search_limit} over official candidate snippets; metrics evaluate top 5",
        "raw_baselines": ["token Jaccard", "BM25"],
        "elapsed_seconds": round(time.perf_counter() - started, 6),
        "mean_candidate_count": round(sum(r["candidate_count"] for r in records) / len(records), 6),
        "mean_snapzip_index_elapsed_seconds": round(sum(index_times) / len(index_times), 6),
        "mean_snapzip_search_elapsed_seconds": round(sum(search_times) / len(search_times), 6),
        "mean_snapzip_diagnostics_elapsed_seconds": safe_mean(diagnostics_search_times),
        "records": records,
    }
    for name in ("jaccard", "bm25", "snapzip"):
        for k in (1, 3, 5):
            hits = sum(1 for record in records if record[f"{name}_hit@{k}"])
            result[f"{name}_hits@{k}"] = hits
            result[f"{name}_acc@{k}"] = hits / len(records)
        result[f"{name}_mrr@5"] = round(sum(record[f"{name}_rr@5"] for record in records) / len(records), 6)
        result[f"{name}_ndcg@5"] = round(sum(record[f"{name}_ndcg@5"] for record in records) / len(records), 6)
        result[f"{name}_duplicate_top5_records"] = sum(1 for record in records if record[f"{name}_duplicate_count@5"] > 0)
        result[f"{name}_duplicate_top5_slots"] = sum(record[f"{name}_duplicate_count@5"] for record in records)
    result["passed"] = result["snapzip_hits@1"] >= result["bm25_hits@1"]
    return result


def validate_repobench_r_values(configs, splits):
    unknown_configs = [config for config in configs if config not in REPOBENCH_R_CONFIGS]
    if unknown_configs:
        raise SystemExit(f"unsupported RepoBench-R config(s): {', '.join(unknown_configs)}")
    unknown_splits = [split for split in splits if split not in REPOBENCH_R_SPLITS]
    if unknown_splits:
        raise SystemExit(f"unsupported RepoBench-R split(s): {', '.join(unknown_splits)}")


def validate_repobench_matrix_data_source(args, configs):
    data_source = args.repobench_data or os.environ.get("REPOBENCH_R_DATA") or ""
    if not data_source or len(configs) <= 1:
        return
    path = Path(data_source).expanduser()
    if path.is_file():
        raise SystemExit(
            "--repobench-data/REPOBENCH_R_DATA points at one file, but matrix mode needs multiple configs. "
            "Pass a directory containing data/<config>.gz files or restrict --repobench-matrix-configs to one config."
        )


def summarize_repobench_r_matrix(runs):
    fields = [
        "snapzip_acc@1",
        "snapzip_acc@3",
        "snapzip_acc@5",
        "snapzip_mrr@5",
        "snapzip_ndcg@5",
        "bm25_acc@5",
        "jaccard_acc@5",
        "mean_snapzip_search_elapsed_seconds",
    ]
    rows = []
    for run in runs:
        row = {
            "config": run["config"],
            "split": run["split"],
            "language": run["language"],
            "sample_size": run["sample_size"],
            "sample_mode": run["sample_mode"],
            "passed": run["passed"],
        }
        for field in fields:
            row[field] = run.get(field)
        row["snapzip_acc@5_over_bm25"] = round(run["snapzip_acc@5"] - run["bm25_acc@5"], 6)
        row["snapzip_acc@5_over_jaccard"] = round(run["snapzip_acc@5"] - run["jaccard_acc@5"], 6)
        rows.append(row)

    macro = {}
    for field in fields + ["snapzip_acc@5_over_bm25", "snapzip_acc@5_over_jaccard"]:
        macro[field] = safe_mean([row[field] for row in rows if row.get(field) is not None])
    macro["sample_size"] = sum(row["sample_size"] for row in rows)
    macro["passed_runs"] = sum(1 for row in rows if row["passed"])
    macro["total_runs"] = len(rows)
    return {"rows": rows, "macro": macro}


def run_repobench_r_matrix(parent, args, snapzip_bin):
    configs = comma_list(args.repobench_matrix_configs, REPOBENCH_R_CONFIGS)
    splits = comma_list(args.repobench_matrix_splits, REPOBENCH_R_SPLITS)
    validate_repobench_r_values(configs, splits)
    validate_repobench_matrix_data_source(args, configs)

    runs = []
    started = time.perf_counter()
    for config in configs:
        for split in splits:
            runs.append(
                run_repobench_r(
                    parent,
                    args,
                    snapzip_bin,
                    config=config,
                    split=split,
                    name=f"repobench_r_{config}_{split}",
                )
            )

    return {
        "name": "repobench_r_matrix",
        "dataset": "tianyang/repobench-r",
        "configs": configs,
        "splits": splits,
        "sample_size_arg": args.repobench_sample_size,
        "sample_seed": args.repobench_seed,
        "elapsed_seconds": round(time.perf_counter() - started, 6),
        "summary": summarize_repobench_r_matrix(runs),
        "runs": runs,
        "passed": all(run["passed"] for run in runs),
    }


def prepare_repobench_p_case(case_no, row_idx, row, work_dir, language, ext, snapzip_bin, args):
    context = row.get("context") or []
    if not context:
        return None
    case_dir = work_dir / f"case_{case_no:03d}_row_{row_idx:05d}"
    source_dir = case_dir / "snippets"
    db_dir = case_dir / "db"
    candidate_texts = repobench_p_context_texts(row, language)
    for snippet_idx, candidate_text in enumerate(candidate_texts):
        rel_path = context[snippet_idx].get("path") or ""
        parts = safe_relative_path_parts(str(Path(rel_path).parent))
        dest_dir = source_dir.joinpath(*parts)
        write_file(dest_dir / f"candidate_{snippet_idx:03d}{ext}", candidate_text)

    _, index_record = run_cmd(
        [
            snapzip_bin,
            "index",
            "--reset",
            "--db-dir",
            str(db_dir),
            "--crawl",
            str(source_dir),
            "--langs",
            language,
        ],
        REPO_ROOT,
    )
    query = repobench_p_query(row)
    file_path = row.get("file_path") or ""
    injected_query = f"--current-path:{file_path}\n{query}" if file_path else query
    diagnostics_limit = requested_snapzip_diagnostics_limit(args)
    main_search_includes_diagnostics = args.snapzip_diagnostics and diagnostics_limit <= args.snapzip_search_limit
    search_cmd = snapzip_eval_search_command(
        snapzip_bin,
        db_dir,
        injected_query,
        args.snapzip_search_limit,
        diagnostics=main_search_includes_diagnostics,
        rerank_cmd=args.snapzip_rerank_cmd,
    )

    _, search_record = run_cmd(search_cmd, REPO_ROOT)
    diagnostics_record = search_record
    if args.snapzip_diagnostics and diagnostics_limit > args.snapzip_search_limit:
        diagnostics_cmd = snapzip_eval_search_command(
            snapzip_bin,
            db_dir,
            injected_query,
            diagnostics_limit,
            diagnostics=True,
            rerank_cmd=args.snapzip_rerank_cmd,
        )
        _, diagnostics_record = run_cmd(diagnostics_cmd, REPO_ROOT)
    snapzip_top, snapzip_return_count, snapzip_receipt_count = snapzip_top5_from_search(search_record["stdout"])
    _, snapzip_diagnostics_return_count, snapzip_diagnostics_receipt_count = snapzip_top5_from_search(
        diagnostics_record["stdout"]
    )
    gold = int(row.get("gold_snippet_index", -1))
    raw_prompt = repobench_p_raw_prompt(row)
    next_line_tokens = completion_support_tokens(row.get("next_line") or "")
    raw_tokens = set(completion_support_tokens(raw_prompt))
    new_next_line_tokens = [token for token in next_line_tokens if token not in raw_tokens]
    gold_identifier = ""
    if 0 <= gold < len(context):
        gold_identifier = context[gold].get("identifier") or ""

    return {
        "case": case_no,
        "dataset_row_index": row_idx,
        "row": row,
        "context": context,
        "candidate_texts": candidate_texts,
        "query": query,
        "raw_prompt": raw_prompt,
        "next_line_tokens": next_line_tokens,
        "new_next_line_tokens": new_next_line_tokens,
        "gold": gold,
        "gold_identifier": gold_identifier,
        "snapzip_top5": snapzip_top,
        "snapzip_return_count": snapzip_return_count,
        "snapzip_receipt_count": snapzip_receipt_count,
        "snapzip_diagnostics_limit": diagnostics_limit if args.snapzip_diagnostics else 0,
        "snapzip_diagnostics_return_count": snapzip_diagnostics_return_count if args.snapzip_diagnostics else 0,
        "snapzip_diagnostics_receipt_count": snapzip_diagnostics_receipt_count if args.snapzip_diagnostics else 0,
        "snapzip_diagnostics_elapsed_seconds": diagnostics_record["elapsed_seconds"] if args.snapzip_diagnostics else 0.0,
        "snapzip_diagnostics": snapzip_diagnostics_from_search(diagnostics_record["stdout"], diagnostics_limit) if args.snapzip_diagnostics else [],
        "index_record": index_record,
        "search_record": search_record,
        "diagnostics_record": diagnostics_record,
    }


def evaluate_case(case_no, row_idx, row, work_dir, language, ext, snapzip_bin, args):
    prepared = prepare_repobench_p_case(case_no, row_idx, row, work_dir, language, ext, snapzip_bin, args)
    if prepared is None:
        return None

    candidate_texts = prepared["candidate_texts"]
    query = prepared["query"]
    jaccard_top = jaccard_top5(query, candidate_texts)
    bm25_top = bm25_top5(query, candidate_texts)
    random_top = deterministic_random_top5(len(candidate_texts), args.repobench_p_seed + row_idx)
    snapzip_top = prepared["snapzip_top5"]
    gold = prepared["gold"]
    row = prepared["row"]

    record = {
        "case": case_no,
        "dataset_row_index": row_idx,
        "repo_name": row.get("repo_name", ""),
        "file_path": row.get("file_path", ""),
        "candidate_count": len(prepared["context"]),
        "query_imports_and_last_3_lines": query,
        "next_line": row.get("next_line", ""),
        "gold_snippet_index": gold,
        "gold_identifier": prepared["gold_identifier"],
        "new_next_line_tokens": prepared["new_next_line_tokens"],
        "jaccard_top5": jaccard_top,
        "bm25_top5": bm25_top,
        "random_top5": random_top,
        "snapzip_top5": snapzip_top,
        "snapzip_return_count": prepared["snapzip_return_count"],
        "snapzip_receipt_count": prepared["snapzip_receipt_count"],
        "snapzip_diagnostics_limit": prepared["snapzip_diagnostics_limit"],
        "snapzip_diagnostics_return_count": prepared["snapzip_diagnostics_return_count"],
        "snapzip_diagnostics_receipt_count": prepared["snapzip_diagnostics_receipt_count"],
        "snapzip_diagnostics_elapsed_seconds": prepared["snapzip_diagnostics_elapsed_seconds"],
        "raw_new_token_coverage@5": 0.0,
        "index_elapsed_seconds": prepared["index_record"]["elapsed_seconds"],
        "search_elapsed_seconds": prepared["search_record"]["elapsed_seconds"],
    }
    if args.snapzip_diagnostics:
        record["snapzip_diagnostics"] = prepared["snapzip_diagnostics"]
    for name in ("random", "jaccard", "bm25", "snapzip"):
        top = record[f"{name}_top5"]
        selected_text = selected_context_text(top[:5], candidate_texts)
        record[f"{name}_gold_rank"] = gold_rank(gold, top) if gold >= 0 else 0
        record[f"{name}_rr@5"] = round(reciprocal_rank(gold, top, 5), 6) if gold >= 0 else 0.0
        record[f"{name}_ndcg@5"] = round(ndcg_at_k(gold, top, 5), 6) if gold >= 0 else 0.0
        record[f"{name}_duplicate_count@5"] = duplicate_result_count(top[:5])
        record[f"{name}_new_token_coverage@5"] = round(token_coverage(prepared["new_next_line_tokens"], selected_text), 6)
        record[f"{name}_identifier_hit@5"] = bool(prepared["gold_identifier"] and prepared["gold_identifier"] in selected_text)
        for k in (1, 3, 5):
            record[f"{name}_gold_hit@{k}"] = gold >= 0 and gold in top[:k]
    return record

def run_repobench_p(parent, args, snapzip_bin):
    from concurrent.futures import ThreadPoolExecutor

    rows, data_source, data_paths = load_repobench_p_rows(args)
    sample_size = min(args.repobench_p_sample_size, len(rows))
    if sample_size <= 0:
        raise SystemExit("--repobench-p-sample-size must be greater than zero")
    sample_indices = sorted(random.Random(args.repobench_p_seed).sample(range(len(rows)), sample_size))
    work_dir = parent / "repobench_p"
    if work_dir.exists():
        shutil.rmtree(work_dir)
    work_dir.mkdir(parents=True, exist_ok=True)

    language = args.repobench_p_language
    ext = ".py" if language == "python" else ".java"
    records = []
    index_times = []
    search_times = []
    started = time.perf_counter()

    max_workers = min(16, os.cpu_count() or 4)
    print(f"Running RepoBench-P evaluation with {max_workers} parallel workers...", file=sys.stderr)

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        futures = []
        for case_no, row_idx in enumerate(sample_indices):
            row = rows[row_idx]
            futures.append(
                executor.submit(
                    evaluate_case,
                    case_no,
                    row_idx,
                    row,
                    work_dir,
                    language,
                    ext,
                    snapzip_bin,
                    args,
                )
            )

        for idx, fut in enumerate(futures, start=1):
            record = fut.result()
            if record is not None:
                records.append(record)
                index_times.append(record["index_elapsed_seconds"])
                search_times.append(record["search_elapsed_seconds"])
            if idx == len(futures) or idx % 25 == 0:
                print(f"Evaluated {idx}/{len(futures)} RepoBench-P cases", file=sys.stderr)

    if not records:
        raise SystemExit("RepoBench v1.1 sample produced no usable rows")

    result = {
        "name": "repobench_p",
        "dataset": "RepoBench v1.1",
        "data_source": data_source,
        "data_paths": report_paths(data_paths),
        "language": language,
        "split": repobench_p_split_name(args.repobench_p_split),
        "sample_size": len(records),
        "sample_seed": args.repobench_p_seed,
        "sample_indices": sample_indices,
        "query": "import statements plus last 3 lines of cropped in-file code",
        "snapzip_search_limit": args.snapzip_search_limit,
        "snapzip_diagnostics_limit": requested_snapzip_diagnostics_limit(args),
        "snapzip_command": f"snapzip search --json --limit {args.snapzip_search_limit} over public cross-file context snippets; metrics evaluate top 5",
        "proxy_metric": "gold cross-file snippet retrieval and coverage of next-line tokens absent from raw prompt",
        "raw_baselines": ["no cross-file context", "random top-5", "token Jaccard", "BM25"],
        "elapsed_seconds": round(time.perf_counter() - started, 6),
        "mean_candidate_count": safe_mean([record["candidate_count"] for record in records]),
        "mean_snapzip_index_elapsed_seconds": safe_mean(index_times),
        "mean_snapzip_search_elapsed_seconds": safe_mean(search_times),
        "mean_snapzip_diagnostics_elapsed_seconds": safe_mean(
            [record["snapzip_diagnostics_elapsed_seconds"] for record in records if record["snapzip_diagnostics_elapsed_seconds"] > 0]
        ),
        "records": records,
    }
    for name in ("random", "jaccard", "bm25", "snapzip"):
        for k in (1, 3, 5):
            hits = sum(1 for record in records if record[f"{name}_gold_hit@{k}"])
            result[f"{name}_gold_hits@{k}"] = hits
            result[f"{name}_gold_hit@{k}"] = hits / len(records)
        result[f"{name}_mrr@5"] = safe_mean([record[f"{name}_rr@5"] for record in records])
        result[f"{name}_ndcg@5"] = safe_mean([record[f"{name}_ndcg@5"] for record in records])
        result[f"{name}_new_token_coverage@5"] = safe_mean(
            [record[f"{name}_new_token_coverage@5"] for record in records]
        )
        result[f"{name}_identifier_hit@5"] = safe_mean(
            [1.0 if record[f"{name}_identifier_hit@5"] else 0.0 for record in records]
        )
        result[f"{name}_duplicate_top5_records"] = sum(
            1 for record in records if record[f"{name}_duplicate_count@5"] > 0
        )
        result[f"{name}_duplicate_top5_slots"] = sum(record[f"{name}_duplicate_count@5"] for record in records)
    result["raw_new_token_coverage@5"] = 0.0
    result["passed"] = (
        result["snapzip_gold_hit@5"] >= result["bm25_gold_hit@5"]
        and result["snapzip_new_token_coverage@5"] >= result["bm25_new_token_coverage@5"]
    )
    return result


def live_cache_path(args):
    if args.live_cache:
        return Path(args.live_cache).expanduser()
    return REPO_ROOT / "benchmarks" / ".work" / "live-model-cache.json"


def load_live_cache(args):
    path = live_cache_path(args)
    if not path.exists():
        return {}, path
    try:
        return json.loads(path.read_text(encoding="utf-8")), path
    except json.JSONDecodeError:
        return {}, path


def save_live_cache(cache, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(cache, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def live_completion_cache_key(cli_cmd, model, system_prompt, user_prompt):
    payload = {
        "provider": "cli",
        "model": model,
        "cli_cmd": cli_cmd,
        "system": system_prompt,
        "user": user_prompt,
    }
    encoded = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def live_cli_command(args):
    cli_cmd = args.live_cli_cmd or os.environ.get("SNAPZIP_LIVE_CLI_CMD") or ""
    if not cli_cmd.strip():
        raise SystemExit(
            "--live-cli-cmd is required for --suite repobench-live. "
            "Pass a local model CLI command that reads the prompt from stdin, "
            "or set SNAPZIP_LIVE_CLI_CMD."
        )
    return cli_cmd


def run_live_cli(cli_cmd, system_prompt, user_prompt, timeout):
    prompt = system_prompt + "\n\n" + user_prompt
    input_text = prompt
    command = cli_cmd
    temp_context = None
    if "{prompt_file}" in command:
        temp_context = tempfile.TemporaryDirectory(prefix="snapzip-live-prompt-")
        prompt_path = Path(temp_context.name) / "prompt.txt"
        prompt_path.write_text(prompt, encoding="utf-8")
        command = command.replace("{prompt_file}", shlex.quote(str(prompt_path)))
        input_text = None
    elif "{prompt}" in command:
        command = command.replace("{prompt}", shlex.quote(prompt))
        input_text = None

    try:
        started = time.perf_counter()
        proc = subprocess.run(
            command,
            cwd=REPO_ROOT,
            shell=True,
            input=input_text,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        elapsed = time.perf_counter() - started
    finally:
        if temp_context is not None:
            temp_context.cleanup()

    if proc.returncode != 0:
        stderr = (proc.stderr or "").strip()
        stdout = (proc.stdout or "").strip()
        detail = stderr or stdout or f"exit code {proc.returncode}"
        if len(detail) > 1200:
            detail = detail[:1200] + "...<truncated>"
        raise SystemExit(f"live model CLI command failed: {detail}")

    return {
        "text": proc.stdout or "",
        "elapsed_seconds": round(elapsed, 6),
        "stderr": proc.stderr or "",
        "cached": False,
    }


def live_complete(args, cache, cli_cmd, model, system_prompt, user_prompt):
    key = live_completion_cache_key(
        cli_cmd,
        model,
        system_prompt,
        user_prompt,
    )
    if not args.live_no_cache and key in cache:
        cached = dict(cache[key])
        cached["cached"] = True
        cached["elapsed_seconds"] = 0.0
        return cached

    result = run_live_cli(cli_cmd, system_prompt, user_prompt, args.live_timeout_seconds)

    if not args.live_no_cache:
        cache[key] = {
            "text": result["text"],
            "provider": "cli",
            "model": model,
        }
    return result


def live_completion_system_prompt():
    return (
        "You are evaluating code completion. Predict the single next line of code at the cursor. "
        "Return only that next line. Preserve indentation when it is clear. Do not explain. "
        "Do not use Markdown fences."
    )


def live_raw_prompt(row, language):
    file_path = row.get("file_path") or ""
    return "\n".join(
        part
        for part in [
            f"Language: {language}",
            f"File: {file_path}",
            "Code before cursor:",
            "```",
            repobench_p_raw_prompt(row),
            "```",
            "Return only the next line.",
        ]
        if part != ""
    )


def live_assisted_prompt(row, language, context_text):
    file_path = row.get("file_path") or ""
    return "\n".join(
        part
        for part in [
            f"Language: {language}",
            f"File: {file_path}",
            "Relevant cross-file context selected by SnapZip:",
            "```",
            context_text.rstrip(),
            "```",
            "Code before cursor:",
            "```",
            repobench_p_raw_prompt(row),
            "```",
            "Return only the next line.",
        ]
        if part != ""
    )


def first_completion_line(text):
    cleaned = (text or "").strip("\n")
    lines = []
    for line in cleaned.splitlines():
        stripped = line.strip()
        if stripped.startswith("```"):
            continue
        lines.append(line.rstrip())
    if not lines:
        return ""
    return lines[0].rstrip()


def score_completion(predicted, expected):
    first_line = first_completion_line(predicted)
    expected_line = (expected or "").rstrip()
    return {
        "prediction": first_line,
        "exact": first_line == expected_line,
        "trimmed_exact": first_line.strip() == expected_line.strip(),
        "token_f1": round(token_f1(first_line, expected_line), 6),
    }


def run_repobench_live(parent, args, snapzip_bin):
    cli_cmd = live_cli_command(args)
    rows, data_source, data_paths = load_repobench_p_rows(args)
    sample_size = min(args.live_sample_size, len(rows))
    if sample_size <= 0:
        raise SystemExit("--live-sample-size must be greater than zero")
    sample_indices = sorted(random.Random(args.live_seed).sample(range(len(rows)), sample_size))
    work_dir = parent / "repobench_live"
    if work_dir.exists():
        shutil.rmtree(work_dir)
    work_dir.mkdir(parents=True, exist_ok=True)

    language = args.repobench_p_language
    ext = ".py" if language == "python" else ".java"
    model = args.live_model or os.environ.get("SNAPZIP_LIVE_MODEL") or "cli"
    system_prompt = live_completion_system_prompt()
    cache, cache_path = load_live_cache(args)

    records = []
    index_times = []
    search_times = []
    raw_call_times = []
    assisted_call_times = []
    started = time.perf_counter()
    for case_no, row_idx in enumerate(sample_indices):
        row = rows[row_idx]
        prepared = prepare_repobench_p_case(case_no, row_idx, row, work_dir, language, ext, snapzip_bin, args)
        if prepared is None:
            continue

        context_text = selected_context_text(prepared["snapzip_top5"][: args.live_context_top_k], prepared["candidate_texts"])
        raw_prompt = live_raw_prompt(row, language)
        assisted_prompt = live_assisted_prompt(row, language, context_text)
        raw_result = live_complete(args, cache, cli_cmd, model, system_prompt, raw_prompt)
        assisted_result = live_complete(args, cache, cli_cmd, model, system_prompt, assisted_prompt)
        if not args.live_no_cache:
            save_live_cache(cache, cache_path)

        raw_score = score_completion(raw_result["text"], row.get("next_line") or "")
        assisted_score = score_completion(assisted_result["text"], row.get("next_line") or "")
        record = {
            "case": case_no,
            "dataset_row_index": row_idx,
            "repo_name": row.get("repo_name", ""),
            "file_path": row.get("file_path", ""),
            "candidate_count": len(prepared["context"]),
            "gold_snippet_index": prepared["gold"],
            "gold_identifier": prepared["gold_identifier"],
            "snapzip_top5": prepared["snapzip_top5"],
            "snapzip_gold_rank": gold_rank(prepared["gold"], prepared["snapzip_top5"]),
            "snapzip_receipt_count": prepared["snapzip_receipt_count"],
            "expected_next_line": (row.get("next_line") or "").rstrip(),
            "raw_prediction": raw_score["prediction"],
            "assisted_prediction": assisted_score["prediction"],
            "raw_exact": raw_score["exact"],
            "assisted_exact": assisted_score["exact"],
            "raw_trimmed_exact": raw_score["trimmed_exact"],
            "assisted_trimmed_exact": assisted_score["trimmed_exact"],
            "raw_token_f1": raw_score["token_f1"],
            "assisted_token_f1": assisted_score["token_f1"],
            "raw_cached": raw_result.get("cached", False),
            "assisted_cached": assisted_result.get("cached", False),
            "index_elapsed_seconds": prepared["index_record"]["elapsed_seconds"],
            "search_elapsed_seconds": prepared["search_record"]["elapsed_seconds"],
            "raw_model_elapsed_seconds": raw_result.get("elapsed_seconds", 0.0),
            "assisted_model_elapsed_seconds": assisted_result.get("elapsed_seconds", 0.0),
        }
        records.append(record)
        index_times.append(record["index_elapsed_seconds"])
        search_times.append(record["search_elapsed_seconds"])
        raw_call_times.append(record["raw_model_elapsed_seconds"])
        assisted_call_times.append(record["assisted_model_elapsed_seconds"])

    if not records:
        raise SystemExit("RepoBench live sample produced no usable rows")

    raw_exact = safe_mean([1.0 if record["raw_exact"] else 0.0 for record in records])
    assisted_exact = safe_mean([1.0 if record["assisted_exact"] else 0.0 for record in records])
    raw_trimmed = safe_mean([1.0 if record["raw_trimmed_exact"] else 0.0 for record in records])
    assisted_trimmed = safe_mean([1.0 if record["assisted_trimmed_exact"] else 0.0 for record in records])
    raw_f1 = safe_mean([record["raw_token_f1"] for record in records])
    assisted_f1 = safe_mean([record["assisted_token_f1"] for record in records])
    result = {
        "name": "repobench_live",
        "dataset": "RepoBench v1.1",
        "data_source": data_source,
        "data_paths": report_paths(data_paths),
        "language": language,
        "split": repobench_p_split_name(args.repobench_p_split),
        "sample_size": len(records),
        "sample_seed": args.live_seed,
        "sample_indices": sample_indices,
        "provider": "cli",
        "model": model,
        "cli_command_configured": True,
        "cache_path": report_path(cache_path),
        "query": "live model next-line completion, raw prompt versus SnapZip-assisted prompt",
        "elapsed_seconds": round(time.perf_counter() - started, 6),
        "mean_candidate_count": safe_mean([record["candidate_count"] for record in records]),
        "mean_snapzip_index_elapsed_seconds": safe_mean(index_times),
        "mean_snapzip_search_elapsed_seconds": safe_mean(search_times),
        "mean_raw_model_elapsed_seconds": safe_mean(raw_call_times),
        "mean_assisted_model_elapsed_seconds": safe_mean(assisted_call_times),
        "raw_exact": raw_exact,
        "assisted_exact": assisted_exact,
        "assisted_exact_delta": round(assisted_exact - raw_exact, 6),
        "raw_trimmed_exact": raw_trimmed,
        "assisted_trimmed_exact": assisted_trimmed,
        "assisted_trimmed_exact_delta": round(assisted_trimmed - raw_trimmed, 6),
        "raw_token_f1": raw_f1,
        "assisted_token_f1": assisted_f1,
        "assisted_token_f1_delta": round(assisted_f1 - raw_f1, 6),
        "raw_cached_calls": sum(1 for record in records if record["raw_cached"]),
        "assisted_cached_calls": sum(1 for record in records if record["assisted_cached"]),
        "records": records,
    }
    result["passed"] = (
        result["assisted_trimmed_exact"] >= result["raw_trimmed_exact"]
        and result["assisted_token_f1"] >= result["raw_token_f1"]
    )
    return result


def snapzip_passed(result):
    if result["name"] == "algorithm_20":
        harness = result["snapzip"]["harness"]
        return harness["passed"] == harness["total"]
    if result["name"] == "hard_rbt":
        return bool(result["snapzip"]["stress"]["result"].get("passed"))
    if result["name"] == "repair_retrieval":
        return bool(result.get("passed"))
    if result["name"] == "context_quality":
        return bool(result.get("passed"))
    if result["name"] == "repobench_r":
        return bool(result.get("passed"))
    if result["name"] == "repobench_r_matrix":
        return bool(result.get("passed"))
    if result["name"] == "repobench_p":
        return bool(result.get("passed"))
    if result["name"] == "repobench_live":
        return bool(result.get("passed"))
    return False


def requested_repobench_quality_gates(args):
    return [
        {
            "name": "repobench.snapzip_acc@1",
            "metric": "snapzip_acc@1",
            "minimum": args.min_repobench_snapzip_acc1,
        },
        {
            "name": "repobench.snapzip_acc@3",
            "metric": "snapzip_acc@3",
            "minimum": args.min_repobench_snapzip_acc3,
        },
        {
            "name": "repobench.snapzip_acc@5",
            "metric": "snapzip_acc@5",
            "minimum": args.min_repobench_snapzip_acc5,
        },
        {
            "name": "repobench.snapzip_mrr@5",
            "metric": "snapzip_mrr@5",
            "minimum": args.min_repobench_snapzip_mrr5,
        },
        {
            "name": "repobench.snapzip_ndcg@5",
            "metric": "snapzip_ndcg@5",
            "minimum": args.min_repobench_snapzip_ndcg5,
        },
        {
            "name": "repobench.snapzip_duplicate_top5_records",
            "metric": "snapzip_duplicate_top5_records",
            "maximum": args.max_repobench_snapzip_duplicate_top5_records,
        },
        {
            "name": "repobench.snapzip_duplicate_top5_slots",
            "metric": "snapzip_duplicate_top5_slots",
            "maximum": args.max_repobench_snapzip_duplicate_top5_slots,
        },
        {
            "name": "repobench.snapzip_acc@5_over_bm25",
            "metric": "snapzip_acc@5",
            "baseline_metric": "bm25_acc@5",
            "minimum_delta": args.min_repobench_snapzip_acc5_over_bm25,
        },
        {
            "name": "repobench.snapzip_mrr@5_over_bm25",
            "metric": "snapzip_mrr@5",
            "baseline_metric": "bm25_mrr@5",
            "minimum_delta": args.min_repobench_snapzip_mrr5_over_bm25,
        },
        {
            "name": "repobench.snapzip_ndcg@5_over_bm25",
            "metric": "snapzip_ndcg@5",
            "baseline_metric": "bm25_ndcg@5",
            "minimum_delta": args.min_repobench_snapzip_ndcg5_over_bm25,
        },
        {
            "name": "repobench.snapzip_acc@5_over_jaccard",
            "metric": "snapzip_acc@5",
            "baseline_metric": "jaccard_acc@5",
            "minimum_delta": args.min_repobench_snapzip_acc5_over_jaccard,
        },
    ]


def requested_repobench_p_quality_gates(args):
    return [
        {
            "name": "repobench_p.snapzip_gold_hit@5",
            "metric": "snapzip_gold_hit@5",
            "minimum": args.min_repobench_p_snapzip_gold_hit5,
        },
        {
            "name": "repobench_p.snapzip_new_token_coverage@5",
            "metric": "snapzip_new_token_coverage@5",
            "minimum": args.min_repobench_p_snapzip_new_token_coverage5,
        },
        {
            "name": "repobench_p.snapzip_identifier_hit@5",
            "metric": "snapzip_identifier_hit@5",
            "minimum": args.min_repobench_p_snapzip_identifier_hit5,
        },
        {
            "name": "repobench_p.snapzip_gold_hit@5_over_bm25",
            "metric": "snapzip_gold_hit@5",
            "baseline_metric": "bm25_gold_hit@5",
            "minimum_delta": args.min_repobench_p_snapzip_gold_hit5_over_bm25,
        },
        {
            "name": "repobench_p.snapzip_new_token_coverage@5_over_bm25",
            "metric": "snapzip_new_token_coverage@5",
            "baseline_metric": "bm25_new_token_coverage@5",
            "minimum_delta": args.min_repobench_p_snapzip_new_token_coverage5_over_bm25,
        },
    ]


def requested_gates(gates):
    return [
        gate
        for gate in gates
        if (
            gate.get("minimum") is not None
            or gate.get("maximum") is not None
            or gate.get("minimum_delta") is not None
        )
    ]


def evaluate_quality_gates_for_run(result, run_name, present_gate_name, gates):
    requested = requested_gates(gates)
    if not requested:
        return []

    run = next((candidate for candidate in result.get("runs", []) if candidate.get("name") == run_name), None)
    if run is None:
        return [
            {
                "name": present_gate_name,
                "metric": run_name,
                "observed": False,
                "minimum": True,
                "passed": False,
            }
        ]

    evaluated = []
    for gate in requested:
        observed = run.get(gate["metric"])
        minimum = gate.get("minimum")
        maximum = gate.get("maximum")
        minimum_delta = gate.get("minimum_delta")
        baseline_metric = gate.get("baseline_metric")
        baseline = None
        delta = None
        passed = observed is not None
        reason = ""
        if observed is None:
            reason = "metric missing"
        if baseline_metric is not None and passed:
            baseline = run.get(baseline_metric)
            if baseline is None:
                passed = False
                reason = "baseline metric missing"
            else:
                delta = round(observed - baseline, 6)
        if minimum is not None and passed:
            passed = passed and observed >= minimum
            if not passed:
                reason = f"observed {observed} below minimum {minimum}"
        if maximum is not None and passed:
            passed = passed and observed <= maximum
            if not passed:
                reason = f"observed {observed} above maximum {maximum}"
        if minimum_delta is not None and passed:
            passed = passed and delta >= minimum_delta
            if not passed:
                reason = f"delta {delta} below minimum_delta {minimum_delta}"
        evaluated.append({
            "name": gate["name"],
            "metric": gate["metric"],
            "observed": observed,
            "baseline_metric": baseline_metric,
            "baseline": baseline,
            "delta": delta,
            "minimum": minimum,
            "maximum": maximum,
            "minimum_delta": minimum_delta,
            "passed": passed,
            "reason": reason,
        })
    return evaluated


def apply_quality_gates(result, args):
    gates = []
    gates.extend(
        evaluate_quality_gates_for_run(
            result,
            "repobench_r",
            "repobench.present",
            requested_repobench_quality_gates(args),
        )
    )
    gates.extend(
        evaluate_quality_gates_for_run(
            result,
            "repobench_p",
            "repobench_p.present",
            requested_repobench_p_quality_gates(args),
        )
    )
    if not gates:
        return result
    result["quality_gates"] = gates
    if not all(gate["passed"] for gate in gates):
        result["passed"] = False
    return result


def main():
    parser = argparse.ArgumentParser(description="Run reproducible SnapZip benchmark comparisons.")
    parser.add_argument(
        "--suite",
        choices=[
            "smoke",
            "algorithm-20",
            "hard-rbt",
            "repair-retrieval",
            "context-quality",
            "repobench-r",
            "repobench-r-matrix",
            "repobench-p",
            "repobench-live",
            "all",
        ],
        default="smoke",
    )
    parser.add_argument("--snapzip-bin", default="", help="Path to a built snapzip binary")
    parser.add_argument("--iterations", type=int, default=100)
    parser.add_argument("--json", default="", help="Optional path to write the JSON report")
    parser.add_argument("--keep-workdir", default="", help="Optional directory to keep generated benchmark files")
    parser.add_argument("--repobench-data", default="", help="Path to a RepoBench-R data/<config>.gz file or data directory")
    parser.add_argument("--repobench-config", default="python_cff")
    parser.add_argument("--repobench-split", choices=["easy", "hard"], default="hard")
    parser.add_argument("--repobench-sample-size", type=int, default=100, help="RepoBench-R sample size; use 0 for the full split")
    parser.add_argument("--repobench-seed", type=int, default=42)
    parser.add_argument("--repobench-matrix-configs", default=",".join(REPOBENCH_R_CONFIGS), help="Comma-separated RepoBench-R configs for matrix mode")
    parser.add_argument("--repobench-matrix-splits", default=",".join(REPOBENCH_R_SPLITS), help="Comma-separated RepoBench-R splits for matrix mode")
    parser.add_argument("--repobench-p-data", default="", help="Path to a RepoBench v1.1 parquet file or split directory")
    parser.add_argument("--repobench-p-language", choices=["python", "java"], default="python")
    parser.add_argument(
        "--repobench-p-split",
        choices=["cross_file_first", "cross_file_random", "in_file", "cff", "cfr", "if"],
        default="cross_file_first",
    )
    parser.add_argument("--repobench-p-sample-size", type=int, default=100)
    parser.add_argument("--repobench-p-seed", type=int, default=42)
    parser.add_argument("--snapzip-rerank-cmd", default="", help="Command to run external reranker in snapzip search")
    parser.add_argument("--snapzip-diagnostics", action="store_true", help="Include compact snapzip search score diagnostics in RepoBench records")
    parser.add_argument("--snapzip-search-limit", type=int, default=5, help="SnapZip search result count for RepoBench runs; metrics still evaluate top 5")
    parser.add_argument(
        "--snapzip-diagnostics-limit",
        type=int,
        default=0,
        help="Separate SnapZip diagnostic search result count; defaults to --snapzip-search-limit",
    )
    parser.add_argument(
        "--repobench-p-max-shards",
        type=int,
        default=1,
        help="Maximum RepoBench v1.1 parquet shards to load from Hugging Face; use 0 for all matching shards",
    )
    parser.add_argument("--min-repobench-snapzip-acc1", type=float, default=None, help="Minimum SnapZip acc@1 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-acc3", type=float, default=None, help="Minimum SnapZip acc@3 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-acc5", type=float, default=None, help="Minimum SnapZip acc@5 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-mrr5", type=float, default=None, help="Minimum SnapZip MRR@5 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-ndcg5", type=float, default=None, help="Minimum SnapZip nDCG@5 for RepoBench-R")
    parser.add_argument("--max-repobench-snapzip-duplicate-top5-records", type=int, default=None, help="Maximum records with duplicate SnapZip top-5 results for RepoBench-R")
    parser.add_argument("--max-repobench-snapzip-duplicate-top5-slots", type=int, default=None, help="Maximum duplicate SnapZip top-5 result slots for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-acc5-over-bm25", type=float, default=None, help="Minimum SnapZip acc@5 delta over BM25 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-mrr5-over-bm25", type=float, default=None, help="Minimum SnapZip MRR@5 delta over BM25 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-ndcg5-over-bm25", type=float, default=None, help="Minimum SnapZip nDCG@5 delta over BM25 for RepoBench-R")
    parser.add_argument("--min-repobench-snapzip-acc5-over-jaccard", type=float, default=None, help="Minimum SnapZip acc@5 delta over Jaccard for RepoBench-R")
    parser.add_argument("--min-repobench-p-snapzip-gold-hit5", type=float, default=None, help="Minimum SnapZip gold hit@5 for RepoBench v1.1")
    parser.add_argument("--min-repobench-p-snapzip-new-token-coverage5", type=float, default=None, help="Minimum SnapZip new-token coverage@5 for RepoBench v1.1")
    parser.add_argument("--min-repobench-p-snapzip-identifier-hit5", type=float, default=None, help="Minimum SnapZip gold-identifier hit@5 for RepoBench v1.1")
    parser.add_argument("--min-repobench-p-snapzip-gold-hit5-over-bm25", type=float, default=None, help="Minimum SnapZip gold hit@5 delta over BM25 for RepoBench v1.1")
    parser.add_argument("--min-repobench-p-snapzip-new-token-coverage5-over-bm25", type=float, default=None, help="Minimum SnapZip new-token coverage@5 delta over BM25 for RepoBench v1.1")
    parser.add_argument("--live-cli-cmd", default="", help="Local model CLI command; receives the prompt on stdin unless it uses {prompt} or {prompt_file}")
    parser.add_argument("--live-model", default="", help="Model label for reports; defaults to SNAPZIP_LIVE_MODEL or cli")
    parser.add_argument("--live-sample-size", type=int, default=20, help="RepoBench live completion sample size")
    parser.add_argument("--live-seed", type=int, default=42, help="RepoBench live completion sample seed")
    parser.add_argument("--live-context-top-k", type=int, default=5, help="SnapZip context snippets to include in assisted prompt")
    parser.add_argument("--live-timeout-seconds", type=float, default=120.0, help="Timeout for each live model CLI call")
    parser.add_argument("--live-cache", default="", help="Optional JSON cache path for live model calls")
    parser.add_argument("--live-no-cache", action="store_true", help="Disable live model response cache")
    args = parser.parse_args()
    if args.snapzip_search_limit < 5:
        raise SystemExit("--snapzip-search-limit must be at least 5 because RepoBench metrics evaluate top-5 results")
    if args.snapzip_diagnostics_limit < 0:
        raise SystemExit("--snapzip-diagnostics-limit must be zero or greater")
    if args.snapzip_diagnostics and 0 < args.snapzip_diagnostics_limit < 5:
        raise SystemExit("--snapzip-diagnostics-limit must be at least 5 when diagnostics are enabled")

    snapzip_bin = resolve_snapzip_bin(args.snapzip_bin)
    started = time.perf_counter()
    result = {
        "suite": args.suite,
        "snapzip_bin": report_path(snapzip_bin),
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
        if args.suite in ("context-quality", "all"):
            result["runs"].append(run_context_quality(work_parent, args, snapzip_bin))
        if args.suite in ("algorithm-20", "all"):
            result["runs"].append(run_algorithm_20(work_parent, args, snapzip_bin))
        if args.suite in ("repobench-r", "all"):
            result["runs"].append(run_repobench_r(work_parent, args, snapzip_bin))
        if args.suite == "repobench-r-matrix":
            result["runs"].append(run_repobench_r_matrix(work_parent, args, snapzip_bin))
        if args.suite in ("repobench-p", "all"):
            result["runs"].append(run_repobench_p(work_parent, args, snapzip_bin))
        if args.suite == "repobench-live":
            result["runs"].append(run_repobench_live(work_parent, args, snapzip_bin))

        result["elapsed_seconds"] = round(time.perf_counter() - started, 6)
        result["passed"] = all(snapzip_passed(run) for run in result["runs"])
        result = apply_quality_gates(result, args)
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
