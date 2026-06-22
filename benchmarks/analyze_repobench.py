#!/usr/bin/env python3
"""Summarize RepoBench retrieval quality and miss patterns.

This script reads JSON produced by benchmarks/run.py and emits a compact,
public-safe analysis of SnapZip versus lexical baselines. It is intended to
guide ranking work before changing weights or retrieval logic.
"""

import argparse
import json
import math
import re
from collections import Counter, defaultdict
from pathlib import Path

from tune_diagnostics import candidate_index_from_path, numeric, safe_mean


DIAGNOSTIC_FIELDS = (
    "qnd",
    "final_score",
    "base_score",
    "lexical_boost",
    "bm25_boost",
    "bm25_rank",
    "bm25f_boost",
    "bm25f_rank",
    "exact_identifier_boost",
    "structured_path_boost",
    "structured_path_rank",
    "structural_rerank_boost",
    "structural_rerank_rank",
    "path_token_boost",
    "path_proximity_boost",
    "path_proximity_rank",
    "structure_boost",
    "rank_fusion_boost",
    "consensus_boost",
    "primary_fts_rank",
    "query_path_rank",
    "lexical_coverage_rank",
    "ordered_token_overlap",
    "tail_token_coverage",
    "tail_ordered_overlap",
    "last_line_token_coverage",
    "call_target_coverage",
    "assignment_lhs_coverage",
    "rare_query_token_coverage",
    "header_token_coverage",
    "query_token_density",
    "candidate_shortness",
    "call_target_declaration_coverage",
    "attribute_declaration_coverage",
    "import_declaration_coverage",
    "tail_declaration_coverage",
    "query_declaration_coverage",
    "query_symbol_coverage",
    "query_symbol_header_coverage",
    "declaration_ordered_overlap",
    "candidate_declaration_density",
    "matched_query_token_count",
)


def load_payload(path):
    with Path(path).expanduser().open("r", encoding="utf-8") as handle:
        return json.load(handle)


def iter_repobench_runs(payload):
    if isinstance(payload, dict):
        records = payload.get("records")
        if isinstance(records, list) and any(isinstance(record, dict) and "gold_snippet_index" in record for record in records):
            yield payload
            return
        for value in payload.values():
            yield from iter_repobench_runs(value)
    elif isinstance(payload, list):
        for item in payload:
            yield from iter_repobench_runs(item)


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


def query_shape(query):
    lines = [line.strip() for line in (query or "").splitlines() if line.strip()]
    if not lines:
        return "empty"

    comment_like = [line for line in lines if line.startswith("#") or line.startswith("//") or line.startswith("*")]
    if len(comment_like) == len(lines):
        return "comment"

    joined = "\n".join(lines)
    if re.search(r"^\s*(import|from|using|use|require|#include)\b", joined, re.MULTILINE):
        return "import"
    if re.search(r"^\s*(async\s+def|def|class|func|function|public|private|protected|static|final)\b", joined, re.MULTILINE):
        return "declaration"
    if re.search(r"\b(except|catch|raise|throw)\b", joined):
        return "exception"
    if re.search(r"(?<![=!<>])=(?![=>])", joined):
        return "assignment"
    if re.search(r"\b[A-Za-z_][A-Za-z0-9_]*\s*\(", joined):
        return "call"

    identifiers = re.findall(r"[A-Za-z_][A-Za-z0-9_]*", joined)
    if len(set(identifier.lower() for identifier in identifiers)) <= 2:
        return "sparse"
    return "mixed"


def diagnostic_items(record):
    items = []
    for fallback_rank, item in enumerate(record.get("snapzip_diagnostics") or [], start=1):
        index = candidate_index_from_path(item.get("path", ""))
        if index is None:
            continue
        diagnostics = dict(item.get("diagnostics") or {})
        diagnostics.update(item.get("benchmark_features") or {})
        diagnostics["matched_query_token_count"] = len(diagnostics.get("matched_query_tokens") or [])
        items.append(
            {
                "index": index,
                "rank": int(numeric(item.get("rank"), fallback_rank)) or fallback_rank,
                "path": item.get("path", ""),
                "score": numeric(item.get("score")),
                "diagnostics": diagnostics,
            }
        )
    items.sort(key=lambda item: (item["rank"], item["index"]))
    return items


def record_query_shape(record, items):
    for item in items:
        intent = item["diagnostics"].get("query_intent")
        if isinstance(intent, str) and intent:
            return intent
    return query_shape(record.get("query_last_3_lines", ""))


def metric_summary(records, name):
    summary = {}
    for k in (1, 3, 5):
        hits = sum(1 for record in records if record["gold_snippet_index"] in (record.get(f"{name}_top5") or [])[:k])
        summary[f"acc@{k}"] = round(hits / len(records), 6) if records else 0.0
    summary["mrr@5"] = round(
        sum(reciprocal_rank(record["gold_snippet_index"], record.get(f"{name}_top5") or [], 5) for record in records) / len(records),
        6,
    ) if records else 0.0
    summary["ndcg@5"] = round(
        sum(ndcg_at_k(record["gold_snippet_index"], record.get(f"{name}_top5") or [], 5) for record in records) / len(records),
        6,
    ) if records else 0.0
    return summary


def format_pattern(pattern):
    labels = ("snapzip", "bm25", "jaccard")
    return ", ".join(label for label, hit in zip(labels, pattern) if hit) or "none"


def analyze_run(run, max_examples):
    records = list(run.get("records") or [])
    hit_patterns = Counter()
    all_shapes = Counter()
    miss_shapes = Counter()
    gold_diagnostic_ranks = []
    diagnostic_miss_count = 0
    diagnostic_recall = Counter()
    diagnostic_recall_by_shape = Counter()
    deltas = defaultdict(list)
    examples = []

    for record in records:
        gold = record.get("gold_snippet_index")
        snap_top = record.get("snapzip_top5") or []
        bm25_top = record.get("bm25_top5") or []
        jaccard_top = record.get("jaccard_top5") or []
        snap_hit = gold in snap_top[:5]
        bm25_hit = gold in bm25_top[:5]
        jaccard_hit = gold in jaccard_top[:5]
        hit_patterns[(snap_hit, bm25_hit, jaccard_hit)] += 1

        items = diagnostic_items(record)
        shape = record_query_shape(record, items)
        all_shapes[shape] += 1
        if not snap_hit:
            miss_shapes[shape] += 1

        gold_item = next((item for item in items if item["index"] == gold), None) if items else None
        if gold_item is not None:
            diagnostic_recall["gold_in_diagnostics"] += 1
            diagnostic_recall_by_shape[(shape, "gold_in_diagnostics")] += 1
            for cutoff in (5, 10, 20):
                if gold_item["rank"] <= cutoff:
                    diagnostic_recall[f"gold_in_diagnostics@{cutoff}"] += 1
        else:
            diagnostic_recall["gold_missing_from_diagnostics"] += 1
            diagnostic_recall_by_shape[(shape, "gold_missing_from_diagnostics")] += 1

        if not snap_hit:
            if gold_item is not None:
                diagnostic_recall["misses_with_gold_in_diagnostics"] += 1
                diagnostic_recall_by_shape[(shape, "misses_with_gold_in_diagnostics")] += 1
                for cutoff in (10, 20):
                    if gold_item["rank"] <= cutoff:
                        diagnostic_recall[f"misses_with_gold_in_diagnostics@{cutoff}"] += 1
            else:
                diagnostic_recall["misses_with_gold_missing_from_diagnostics"] += 1
                diagnostic_recall_by_shape[(shape, "misses_with_gold_missing_from_diagnostics")] += 1

        if not snap_hit and items:
            top = items[0]
            if gold_item is not None:
                diagnostic_miss_count += 1
                gold_diagnostic_ranks.append(gold_item["rank"])
                top_diag = top["diagnostics"]
                gold_diag = gold_item["diagnostics"]
                for field in DIAGNOSTIC_FIELDS:
                    deltas[field].append(numeric(gold_diag.get(field)) - numeric(top_diag.get(field)))

        if not snap_hit and len(examples) < max_examples:
            gold_diag_rank = None
            if gold_item is not None:
                gold_diag_rank = gold_item["rank"]
            examples.append(
                {
                    "case": record.get("case"),
                    "dataset_row_index": record.get("dataset_row_index"),
                    "repo_name": record.get("repo_name"),
                    "file_path": record.get("file_path"),
                    "query_shape": shape,
                    "gold_snippet_index": gold,
                    "snapzip_top5": snap_top[:5],
                    "bm25_top5": bm25_top[:5],
                    "jaccard_top5": jaccard_top[:5],
                    "gold_diagnostic_rank": gold_diag_rank,
                }
            )

    opportunities = {
        "snapzip_misses": sum(count for pattern, count in hit_patterns.items() if not pattern[0]),
        "bm25_or_jaccard_hit_when_snapzip_missed": sum(
            count for pattern, count in hit_patterns.items() if not pattern[0] and (pattern[1] or pattern[2])
        ),
        "bm25_only_hit_when_snapzip_missed": hit_patterns[(False, True, False)],
        "jaccard_only_hit_when_snapzip_missed": hit_patterns[(False, False, True)],
        "both_baselines_hit_when_snapzip_missed": hit_patterns[(False, True, True)],
        "all_three_missed": hit_patterns[(False, False, False)],
        "snapzip_only_hit": hit_patterns[(True, False, False)],
    }

    rank_summary = {}
    if gold_diagnostic_ranks:
        sorted_ranks = sorted(gold_diagnostic_ranks)
        rank_summary = {
            "count": len(sorted_ranks),
            "mean": safe_mean(sorted_ranks),
            "min": sorted_ranks[0],
            "median": sorted_ranks[len(sorted_ranks) // 2],
            "max": sorted_ranks[-1],
        }

    return {
        "name": run.get("name"),
        "dataset": run.get("dataset"),
        "config": run.get("config"),
        "split": run.get("split"),
        "language": run.get("language"),
        "sample_size": len(records),
        "metrics": {
            "snapzip": metric_summary(records, "snapzip"),
            "bm25": metric_summary(records, "bm25"),
            "jaccard": metric_summary(records, "jaccard"),
        },
        "hit_patterns": {format_pattern(pattern): count for pattern, count in sorted(hit_patterns.items())},
        "opportunities": opportunities,
        "query_shapes": {
            "all": dict(all_shapes.most_common()),
            "snapzip_misses": dict(miss_shapes.most_common()),
        },
        "diagnostics": {
            "misses_with_gold_in_diagnostics": diagnostic_miss_count,
            "recall_ceiling": dict(diagnostic_recall),
            "recall_ceiling_by_shape": {
                shape: {kind: diagnostic_recall_by_shape[(shape, kind)] for kind in sorted({key[1] for key in diagnostic_recall_by_shape if key[0] == shape})}
                for shape in sorted({key[0] for key in diagnostic_recall_by_shape})
            },
            "gold_rank_when_missed": rank_summary,
            "gold_minus_top_mean_deltas": {field: safe_mean(values) for field, values in sorted(deltas.items()) if values},
        },
        "examples": examples,
    }


def aggregate_runs(runs):
    if not runs:
        return {}

    total = sum(run["sample_size"] for run in runs)
    metrics = {}
    for system in ("snapzip", "bm25", "jaccard"):
        metrics[system] = {}
        for field in ("acc@1", "acc@3", "acc@5", "mrr@5", "ndcg@5"):
            weighted = sum(run["metrics"][system][field] * run["sample_size"] for run in runs)
            metrics[system][field] = round(weighted / total, 6) if total else 0.0

    opportunities = Counter()
    miss_shapes = Counter()
    all_shapes = Counter()
    recall_ceiling = Counter()
    recall_ceiling_by_shape = defaultdict(Counter)
    blockers = []
    for run in runs:
        opportunities.update(run["opportunities"])
        miss_shapes.update(run["query_shapes"]["snapzip_misses"])
        all_shapes.update(run["query_shapes"]["all"])
        recall_ceiling.update(run["diagnostics"].get("recall_ceiling") or {})
        for shape, values in (run["diagnostics"].get("recall_ceiling_by_shape") or {}).items():
            recall_ceiling_by_shape[shape].update(values)
        snapzip = run["metrics"]["snapzip"]
        bm25 = run["metrics"]["bm25"]
        jaccard = run["metrics"]["jaccard"]
        blocker = {
            "config": run.get("config"),
            "split": run.get("split"),
            "language": run.get("language"),
            "sample_size": run["sample_size"],
            "snapzip_acc@5": snapzip["acc@5"],
            "bm25_acc@5": bm25["acc@5"],
            "jaccard_acc@5": jaccard["acc@5"],
            "snapzip_mrr@5": snapzip["mrr@5"],
            "bm25_mrr@5": bm25["mrr@5"],
            "jaccard_mrr@5": jaccard["mrr@5"],
            "acc@5_over_bm25": round(snapzip["acc@5"] - bm25["acc@5"], 6),
            "acc@5_over_jaccard": round(snapzip["acc@5"] - jaccard["acc@5"], 6),
            "mrr@5_over_bm25": round(snapzip["mrr@5"] - bm25["mrr@5"], 6),
            "mrr@5_over_jaccard": round(snapzip["mrr@5"] - jaccard["mrr@5"], 6),
            "snapzip_misses": run["opportunities"]["snapzip_misses"],
            "recoverable_misses": run["opportunities"]["bm25_or_jaccard_hit_when_snapzip_missed"],
            "all_three_missed": run["opportunities"]["all_three_missed"],
            "top_miss_shape": next(iter(run["query_shapes"]["snapzip_misses"]), ""),
        }
        blocker["worst_acc@5_gap"] = min(blocker["acc@5_over_bm25"], blocker["acc@5_over_jaccard"])
        blocker["worst_mrr@5_gap"] = min(blocker["mrr@5_over_bm25"], blocker["mrr@5_over_jaccard"])
        blockers.append(blocker)

    blockers.sort(key=lambda row: (row["worst_acc@5_gap"], row["worst_mrr@5_gap"], row["config"] or "", row["split"] or ""))
    return {
        "sample_size": total,
        "run_count": len(runs),
        "metrics": metrics,
        "metric_deltas": {
            "snapzip_acc@5_over_bm25": round(metrics["snapzip"]["acc@5"] - metrics["bm25"]["acc@5"], 6),
            "snapzip_acc@5_over_jaccard": round(metrics["snapzip"]["acc@5"] - metrics["jaccard"]["acc@5"], 6),
            "snapzip_mrr@5_over_bm25": round(metrics["snapzip"]["mrr@5"] - metrics["bm25"]["mrr@5"], 6),
            "snapzip_mrr@5_over_jaccard": round(metrics["snapzip"]["mrr@5"] - metrics["jaccard"]["mrr@5"], 6),
        },
        "opportunities": dict(opportunities),
        "query_shapes": {
            "all": dict(all_shapes.most_common()),
            "snapzip_misses": dict(miss_shapes.most_common()),
        },
        "diagnostics": {
            "recall_ceiling": dict(recall_ceiling),
            "recall_ceiling_by_shape": {shape: dict(counter) for shape, counter in sorted(recall_ceiling_by_shape.items())},
        },
        "blockers": blockers,
    }


def markdown_table(headers, rows):
    output = ["| " + " | ".join(headers) + " |", "| " + " | ".join("---" for _ in headers) + " |"]
    for row in rows:
        output.append("| " + " | ".join(str(item) for item in row) + " |")
    return "\n".join(output)


def render_markdown(analysis):
    lines = ["# RepoBench Retrieval Analysis", ""]
    aggregate = analysis.get("aggregate") or {}
    if aggregate:
        lines.extend(["## Aggregate", ""])
        lines.append(
            markdown_table(
                ["system", "acc@1", "acc@3", "acc@5", "mrr@5", "ndcg@5"],
                [
                    [name, values["acc@1"], values["acc@3"], values["acc@5"], values["mrr@5"], values["ndcg@5"]]
                    for name, values in aggregate["metrics"].items()
                ],
            )
        )
        lines.append("")
        lines.append("### Aggregate Deltas")
        lines.append(markdown_table(["metric", "value"], [[key, value] for key, value in aggregate["metric_deltas"].items()]))
        lines.append("")
        lines.append("### Aggregate Miss Opportunities")
        lines.append(markdown_table(["category", "count"], [[key, value] for key, value in aggregate["opportunities"].items()]))
        lines.append("")
        recall_ceiling = aggregate["diagnostics"].get("recall_ceiling") or {}
        if recall_ceiling:
            lines.append("### Aggregate Diagnostic Recall Ceiling")
            lines.append(markdown_table(["category", "count"], [[key, value] for key, value in recall_ceiling.items()]))
            lines.append("")
        recall_by_shape = aggregate["diagnostics"].get("recall_ceiling_by_shape") or {}
        if recall_by_shape:
            lines.append("### Aggregate Diagnostic Recall By Shape")
            lines.append(
                markdown_table(
                    ["shape", "miss_gold_in_diag", "miss_gold_missing", "gold_in_diag", "gold_missing"],
                    [
                        [
                            shape,
                            values.get("misses_with_gold_in_diagnostics", 0),
                            values.get("misses_with_gold_missing_from_diagnostics", 0),
                            values.get("gold_in_diagnostics", 0),
                            values.get("gold_missing_from_diagnostics", 0),
                        ]
                        for shape, values in recall_by_shape.items()
                    ],
                )
            )
            lines.append("")
        lines.append("### Aggregate SnapZip Miss Query Shapes")
        lines.append(
            markdown_table(
                ["shape", "count"],
                [[key, value] for key, value in aggregate["query_shapes"]["snapzip_misses"].items()],
            )
        )
        lines.append("")
        lines.append("### Largest Split Blockers")
        lines.append(
            markdown_table(
                [
                    "config",
                    "split",
                    "acc@5",
                    "vs_bm25",
                    "vs_jaccard",
                    "recoverable",
                    "all_missed",
                    "top_miss_shape",
                ],
                [
                    [
                        row["config"],
                        row["split"],
                        row["snapzip_acc@5"],
                        row["acc@5_over_bm25"],
                        row["acc@5_over_jaccard"],
                        row["recoverable_misses"],
                        row["all_three_missed"],
                        row["top_miss_shape"],
                    ]
                    for row in aggregate["blockers"][:8]
                ],
            )
        )
        lines.append("")

    for run in analysis["runs"]:
        title_parts = [part for part in (run.get("config"), run.get("split"), run.get("language")) if part]
        title = " / ".join(title_parts) or run.get("name") or "run"
        lines.extend([f"## {title}", ""])
        metrics = run["metrics"]
        lines.append(
            markdown_table(
                ["system", "acc@1", "acc@3", "acc@5", "mrr@5", "ndcg@5"],
                [
                    [name, values["acc@1"], values["acc@3"], values["acc@5"], values["mrr@5"], values["ndcg@5"]]
                    for name, values in metrics.items()
                ],
            )
        )
        lines.append("")

        opportunities = run["opportunities"]
        lines.append("### Miss Opportunities")
        lines.append(
            markdown_table(
                ["category", "count"],
                [[key, value] for key, value in opportunities.items()],
            )
        )
        lines.append("")

        lines.append("### Hit Pattern Counts")
        lines.append(markdown_table(["hit pattern", "count"], [[key, value] for key, value in run["hit_patterns"].items()]))
        lines.append("")

        miss_shapes = run["query_shapes"]["snapzip_misses"]
        if miss_shapes:
            lines.append("### SnapZip Miss Query Shapes")
            lines.append(markdown_table(["shape", "count"], [[key, value] for key, value in miss_shapes.items()]))
            lines.append("")

        diagnostics = run["diagnostics"]
        recall_ceiling = diagnostics.get("recall_ceiling") or {}
        if recall_ceiling:
            lines.append("### Diagnostic Recall Ceiling")
            lines.append(markdown_table(["category", "count"], [[key, value] for key, value in recall_ceiling.items()]))
            lines.append("")
        if diagnostics["gold_rank_when_missed"]:
            lines.append("### Diagnostic Misses")
            lines.append(
                markdown_table(
                    ["metric", "value"],
                    [[key, value] for key, value in diagnostics["gold_rank_when_missed"].items()],
                )
            )
            lines.append("")

        deltas = diagnostics["gold_minus_top_mean_deltas"]
        if deltas:
            lines.append("### Gold Minus Top Mean Diagnostic Deltas")
            lines.append(markdown_table(["field", "mean_delta"], [[key, value] for key, value in deltas.items()]))
            lines.append("")

        if run["examples"]:
            lines.append("### Example Misses")
            rows = []
            for example in run["examples"]:
                rows.append(
                    [
                        example["case"],
                        example["query_shape"],
                        example["gold_snippet_index"],
                        example["snapzip_top5"],
                        example["bm25_top5"],
                        example["jaccard_top5"],
                        example["gold_diagnostic_rank"],
                    ]
                )
            lines.append(markdown_table(["case", "shape", "gold", "snapzip", "bm25", "jaccard", "gold_diag_rank"], rows))
            lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def main():
    parser = argparse.ArgumentParser(description="Analyze RepoBench benchmark JSON for retrieval-quality work.")
    parser.add_argument("--input", required=True, help="Benchmark JSON from benchmarks/run.py")
    parser.add_argument("--json", default="", help="Optional path for machine-readable analysis JSON")
    parser.add_argument("--max-examples", type=int, default=8, help="Maximum example misses to include per run")
    args = parser.parse_args()

    payload = load_payload(args.input)
    runs = [analyze_run(run, args.max_examples) for run in iter_repobench_runs(payload)]
    analysis = {
        "input": str(Path(args.input).expanduser()),
        "run_count": len(runs),
        "aggregate": aggregate_runs(runs),
        "runs": runs,
    }

    if args.json:
        output_path = Path(args.json).expanduser()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(analysis, indent=2) + "\n", encoding="utf-8")

    print(render_markdown(analysis))


if __name__ == "__main__":
    main()
