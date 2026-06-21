#!/usr/bin/env python3
"""Offline SnapZip ranking diagnostics tuner.

This script replays benchmark JSON produced with --snapzip-diagnostics and
tests whether alternate score-feature weights would improve ordering within the
returned SnapZip candidates. It does not recover candidates that SnapZip
did not return, so the returned candidate set is the recall ceiling.
"""

import argparse
import json
import math
import re
from pathlib import Path


DEFAULT_FEATURES = (
    "runtime_score",
    "qnd_score",
    "base_score",
    "lexical_boost",
    "bm25_boost",
    "bm25_rank_recip",
    "bm25f_boost",
    "bm25f_rank_recip",
    "exact_identifier_boost",
    "structured_path_boost",
    "structured_path_rank_recip",
    "structural_rerank_boost",
    "structural_rerank_rank_recip",
    "path_token_boost",
    "path_proximity_boost",
    "path_proximity_rank_recip",
    "git_recency_boost",
    "language_boost",
    "structure_boost",
    "topic_penalty_score",
    "rank_fusion_score",
    "rank_fusion_boost",
    "primary_fts_rank_recip",
    "query_path_rank_recip",
    "lexical_coverage_rank_recip",
    "protected_candidate",
    "external_rerank_rank_recip",
    "external_rrf_score",
    "matched_query_token_count",
)

DEFAULT_GRID = (
    0.0,
    0.05,
    0.1,
    0.2,
    0.35,
    0.6,
    1.0,
    1.5,
    2.0,
)

NEGATIVE_GRID = (-1.0, -0.6, -0.35, -0.2, -0.1)


def numeric(value, default=0.0):
    if isinstance(value, bool):
        return 1.0 if value else 0.0
    if isinstance(value, (int, float)):
        if math.isfinite(float(value)):
            return float(value)
        return default
    return default


def reciprocal_rank_feature(value):
    rank = numeric(value, 0.0)
    if rank <= 0:
        return 0.0
    return 1.0 / rank


def candidate_index_from_path(path):
    stem = Path(path or "").stem
    match = re.search(r"(?:snippet|candidate)_([0-9]+)$", stem)
    if match:
        return int(match.group(1))
    match = re.search(r"([0-9]+)$", stem)
    if match:
        return int(match.group(1))
    return None


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


def safe_mean(values):
    if not values:
        return 0.0
    return round(sum(values) / len(values), 6)


def iter_benchmark_records(payload):
    if isinstance(payload, dict):
        if "gold_snippet_index" in payload and "snapzip_top5" in payload:
            yield payload
            return
        for value in payload.values():
            yield from iter_benchmark_records(value)
    elif isinstance(payload, list):
        for item in payload:
            yield from iter_benchmark_records(item)


def raw_features(item):
    diagnostics = item.get("diagnostics") or {}
    final_score = diagnostics.get("final_score", item.get("score", 0.0))
    features = {
        "runtime_score": -numeric(final_score),
        "qnd_score": -numeric(diagnostics.get("qnd")),
        "base_score": -numeric(diagnostics.get("base_score")),
        "lexical_boost": numeric(diagnostics.get("lexical_boost")),
        "bm25_boost": numeric(diagnostics.get("bm25_boost")),
        "bm25_rank_recip": reciprocal_rank_feature(diagnostics.get("bm25_rank")),
        "bm25f_boost": numeric(diagnostics.get("bm25f_boost")),
        "bm25f_rank_recip": reciprocal_rank_feature(diagnostics.get("bm25f_rank")),
        "exact_identifier_boost": numeric(diagnostics.get("exact_identifier_boost")),
        "structured_path_boost": numeric(diagnostics.get("structured_path_boost")),
        "structured_path_rank_recip": reciprocal_rank_feature(diagnostics.get("structured_path_rank")),
        "structural_rerank_boost": numeric(diagnostics.get("structural_rerank_boost")),
        "structural_rerank_rank_recip": reciprocal_rank_feature(diagnostics.get("structural_rerank_rank")),
        "path_token_boost": numeric(diagnostics.get("path_token_boost")),
        "path_proximity_boost": numeric(diagnostics.get("path_proximity_boost")),
        "path_proximity_rank_recip": reciprocal_rank_feature(diagnostics.get("path_proximity_rank")),
        "git_recency_boost": numeric(diagnostics.get("git_recency_boost")),
        "language_boost": numeric(diagnostics.get("language_boost")),
        "structure_boost": numeric(diagnostics.get("structure_boost")),
        "topic_penalty_score": -numeric(diagnostics.get("topic_penalty")),
        "rank_fusion_score": numeric(diagnostics.get("rank_fusion_score")),
        "rank_fusion_boost": numeric(diagnostics.get("rank_fusion_boost")),
        "primary_fts_rank_recip": reciprocal_rank_feature(diagnostics.get("primary_fts_rank")),
        "query_path_rank_recip": reciprocal_rank_feature(diagnostics.get("query_path_rank")),
        "lexical_coverage_rank_recip": reciprocal_rank_feature(diagnostics.get("lexical_coverage_rank")),
        "protected_candidate": numeric(diagnostics.get("protected_candidate")),
        "external_rerank_rank_recip": reciprocal_rank_feature(diagnostics.get("external_rerank_rank")),
        "external_rrf_score": numeric(diagnostics.get("external_rrf_score")),
        "matched_query_token_count": float(len(diagnostics.get("matched_query_tokens") or [])),
    }
    return features


def normalize_candidate_features(candidates, feature_names):
    values_by_feature = {name: [candidate["raw_features"].get(name, 0.0) for candidate in candidates] for name in feature_names}
    for candidate in candidates:
        normalized = {}
        for name, values in values_by_feature.items():
            low = min(values)
            high = max(values)
            value = candidate["raw_features"].get(name, 0.0)
            if high == low:
                normalized[name] = 0.0
            else:
                normalized[name] = (value - low) / (high - low)
        candidate["features"] = normalized


def prepare_records(records, feature_names):
    prepared = []
    skipped_without_diagnostics = 0
    skipped_without_candidates = 0
    for record in records:
        diagnostics = record.get("snapzip_diagnostics") or []
        if not diagnostics:
            skipped_without_diagnostics += 1
            continue
        candidates = []
        seen = set()
        for fallback_rank, item in enumerate(diagnostics, start=1):
            index = candidate_index_from_path(item.get("path", ""))
            if index is None or index in seen:
                continue
            seen.add(index)
            rank = int(numeric(item.get("rank"), fallback_rank)) or fallback_rank
            candidates.append(
                {
                    "index": index,
                    "path": item.get("path", ""),
                    "original_rank": rank,
                    "raw_features": raw_features(item),
                }
            )
        if not candidates:
            skipped_without_candidates += 1
            continue
        candidates.sort(key=lambda candidate: candidate["original_rank"])
        normalize_candidate_features(candidates, feature_names)
        prepared.append(
            {
                "case": record.get("case"),
                "dataset_row_index": record.get("dataset_row_index"),
                "gold": int(numeric(record.get("gold_snippet_index"), -1)),
                "baseline_top5": list(record.get("snapzip_top5") or [])[:5],
                "candidates": candidates,
            }
        )
    return prepared, {
        "skipped_without_diagnostics": skipped_without_diagnostics,
        "skipped_without_candidates": skipped_without_candidates,
    }


def score_candidate(candidate, weights):
    score = 0.0
    for feature, weight in weights.items():
        score += candidate["features"].get(feature, 0.0) * weight
    return score


def reranked_top5(record, weights):
    ranked = sorted(
        record["candidates"],
        key=lambda candidate: (-score_candidate(candidate, weights), candidate["original_rank"], candidate["index"]),
    )
    return [candidate["index"] for candidate in ranked[:5]]


def baseline_top5(record):
    top = [int(item) for item in record["baseline_top5"] if isinstance(item, int) and item >= 0]
    if top:
        return top[:5]
    return [candidate["index"] for candidate in record["candidates"][:5]]


def evaluate_records(records, weights=None):
    tops = []
    for record in records:
        if weights is None:
            top = baseline_top5(record)
        else:
            top = reranked_top5(record, weights)
        tops.append((record["gold"], top))
    total = len(tops)
    if total == 0:
        return {
            "records": 0,
            "hit@1": 0.0,
            "hit@3": 0.0,
            "hit@5": 0.0,
            "mrr@5": 0.0,
            "ndcg@5": 0.0,
        }
    return {
        "records": total,
        "hit@1": safe_mean([1.0 if gold in top[:1] else 0.0 for gold, top in tops]),
        "hit@3": safe_mean([1.0 if gold in top[:3] else 0.0 for gold, top in tops]),
        "hit@5": safe_mean([1.0 if gold in top[:5] else 0.0 for gold, top in tops]),
        "mrr@5": safe_mean([reciprocal_rank(gold, top, 5) for gold, top in tops]),
        "ndcg@5": safe_mean([ndcg_at_k(gold, top, 5) for gold, top in tops]),
    }


def parse_metric_list(value):
    if not value or value.strip().lower() == "none":
        return ()
    return tuple(item.strip() for item in value.split(",") if item.strip())


def acceptable_metrics(candidate, incumbent, guardrails):
    return all(candidate.get(metric, 0.0) >= incumbent.get(metric, 0.0) for metric in guardrails)


def metric_key(metrics, primary, guardrails=()):
    return (
        metrics.get(primary, 0.0),
        metrics.get("hit@1", 0.0),
        metrics.get("hit@3", 0.0),
        metrics.get("mrr@5", 0.0),
        metrics.get("ndcg@5", 0.0),
        *(metrics.get(metric, 0.0) for metric in guardrails),
    )


def split_records(records, validation_fraction):
    if len(records) < 10 or validation_fraction <= 0:
        return records, []
    validation_count = max(1, round(len(records) * validation_fraction))
    validation_count = min(validation_count, len(records) - 1)
    train = []
    validation = []
    for index, record in enumerate(records):
        if (index * 9973) % len(records) < validation_count:
            validation.append(record)
        else:
            train.append(record)
    return train, validation


def clean_weights(weights):
    if not weights:
        return {}
    return {feature: round(weight, 6) for feature, weight in sorted(weights.items()) if abs(weight) > 1e-9}


def starter_profiles():
    return {
        "runtime": {"runtime_score": 1.0},
        "runtime_bm25": {"runtime_score": 1.0, "bm25_rank_recip": 0.4, "bm25f_rank_recip": 0.25},
        "boosts": {
            "runtime_score": 1.0,
            "lexical_boost": 0.4,
            "exact_identifier_boost": 0.5,
            "structured_path_boost": 0.25,
            "structural_rerank_boost": 0.35,
            "path_proximity_boost": 0.25,
            "rank_fusion_boost": 0.3,
        },
        "coverage": {
            "runtime_score": 1.0,
            "matched_query_token_count": 0.5,
            "lexical_coverage_rank_recip": 0.4,
            "bm25_rank_recip": 0.4,
            "query_path_rank_recip": 0.25,
        },
    }


def choose_initial_profile(records, metric, guardrails):
    floor_metrics = evaluate_records(records)
    best_name = "baseline"
    best_weights = None
    best_metrics = floor_metrics
    for name, weights in starter_profiles().items():
        metrics = evaluate_records(records, weights)
        if acceptable_metrics(metrics, floor_metrics, guardrails) and metric_key(metrics, metric, guardrails) > metric_key(
            best_metrics, metric, guardrails
        ):
            best_name = name
            best_weights = dict(weights)
            best_metrics = metrics
    return best_name, best_weights, best_metrics


def tune_weights(records, feature_names, metric, guardrails, passes, grid):
    floor_metrics = evaluate_records(records)
    profile_name, current_weights, current_metrics = choose_initial_profile(records, metric, guardrails)
    history = [
        {
            "profile": profile_name,
            "metrics": current_metrics,
            "weights": clean_weights(current_weights),
        }
    ]
    for pass_index in range(1, passes + 1):
        improved = False
        for feature in feature_names:
            best_feature_weights = dict(current_weights or {})
            best_feature_metrics = current_metrics
            for value in grid:
                trial_weights = dict(current_weights or {})
                if abs(value) <= 1e-12:
                    trial_weights.pop(feature, None)
                else:
                    trial_weights[feature] = value
                trial_metrics = evaluate_records(records, trial_weights)
                if acceptable_metrics(trial_metrics, floor_metrics, guardrails) and metric_key(
                    trial_metrics, metric, guardrails
                ) > metric_key(best_feature_metrics, metric, guardrails):
                    best_feature_weights = trial_weights
                    best_feature_metrics = trial_metrics
            if acceptable_metrics(best_feature_metrics, floor_metrics, guardrails) and metric_key(
                best_feature_metrics, metric, guardrails
            ) > metric_key(current_metrics, metric, guardrails):
                current_weights = best_feature_weights
                current_metrics = best_feature_metrics
                improved = True
        history.append(
            {
                "pass": pass_index,
                "improved": improved,
                "metrics": current_metrics,
                "weights": clean_weights(current_weights),
            }
        )
        if not improved:
            break
    return current_weights, current_metrics, history


def metrics_delta(after, before):
    fields = ("hit@1", "hit@3", "hit@5", "mrr@5", "ndcg@5")
    return {field: round(after.get(field, 0.0) - before.get(field, 0.0), 6) for field in fields}


def validation_summary(metric, guardrails, baseline_validation, tuned_validation):
    if not baseline_validation:
        return {
            "status": "not_run",
            "metric": metric,
            "guardrails": list(guardrails),
            "passes": None,
            "delta_vs_baseline": {},
        }
    delta = metrics_delta(tuned_validation, baseline_validation)
    guardrail_passes = all(delta.get(metric_name, 0.0) >= 0 for metric_name in guardrails)
    metric_passes = delta.get(metric, 0.0) >= 0
    return {
        "status": "pass" if metric_passes and guardrail_passes else "hold",
        "metric": metric,
        "guardrails": list(guardrails),
        "passes": metric_passes and guardrail_passes,
        "delta_vs_baseline": delta,
    }


def changed_cases(records, weights, limit):
    if weights is None:
        return []
    changes = []
    for record in records:
        baseline = baseline_top5(record)
        tuned = reranked_top5(record, weights)
        if baseline == tuned:
            continue
        gold = record["gold"]
        before_rank = gold_rank(gold, baseline)
        after_rank = gold_rank(gold, tuned)
        if before_rank == after_rank:
            continue
        changes.append(
            {
                "case": record.get("case"),
                "dataset_row_index": record.get("dataset_row_index"),
                "gold": gold,
                "baseline_rank": before_rank,
                "tuned_rank": after_rank,
                "baseline_top5": baseline,
                "tuned_top5": tuned,
            }
        )
    changes.sort(
        key=lambda item: (
            99 if item["baseline_rank"] == 0 else item["baseline_rank"],
            item["tuned_rank"] if item["tuned_rank"] > 0 else 99,
        )
    )
    return changes[:limit]


def build_report(args, payload):
    raw_records = list(iter_benchmark_records(payload))
    feature_names = tuple(feature.strip() for feature in args.features.split(",") if feature.strip()) if args.features else DEFAULT_FEATURES
    guardrails = parse_metric_list(args.guardrails)
    prepared, skipped = prepare_records(raw_records, feature_names)
    if not prepared:
        raise SystemExit("no benchmark records with snapzip_diagnostics were found")

    train, validation = split_records(prepared, args.validation_fraction)
    tune_set = train or prepared
    tuned_weights, train_metrics, history = tune_weights(
        tune_set,
        feature_names,
        args.metric,
        guardrails,
        args.passes,
        [float(value) for value in args.grid.split(",")] if args.grid else (NEGATIVE_GRID + DEFAULT_GRID if args.allow_negative else DEFAULT_GRID),
    )

    baseline_all = evaluate_records(prepared)
    tuned_all = evaluate_records(prepared, tuned_weights)
    baseline_train = evaluate_records(train) if train else {}
    baseline_validation = evaluate_records(validation) if validation else {}
    tuned_validation = evaluate_records(validation, tuned_weights) if validation else {}
    validation_decision = validation_summary(args.metric, guardrails, baseline_validation, tuned_validation)

    gold_in_candidates = sum(1 for record in prepared if record["gold"] in [candidate["index"] for candidate in record["candidates"]])
    report = {
        "input": str(args.input),
        "metric": args.metric,
        "guardrails": list(guardrails),
        "feature_count": len(feature_names),
        "features": list(feature_names),
        "record_counts": {
            "raw_records": len(raw_records),
            "diagnostic_records": len(prepared),
            "gold_in_returned_candidates": gold_in_candidates,
            **skipped,
        },
        "limitation": "Diagnostics include only SnapZip's returned candidates, so this tuner can improve ordering inside that set but cannot recover missing candidates.",
        "baseline": {
            "all": baseline_all,
            "train": baseline_train,
            "validation": baseline_validation,
        },
        "tuned": {
            "weights": clean_weights(tuned_weights),
            "train": train_metrics,
            "validation": tuned_validation,
            "all": tuned_all,
            "delta_vs_baseline_all": metrics_delta(tuned_all, baseline_all),
        },
        "validation_decision": validation_decision,
        "history": history,
        "changed_cases": changed_cases(prepared, tuned_weights, args.top_moves),
    }
    return report


def print_report(report):
    counts = report["record_counts"]
    print(f"Loaded {counts['diagnostic_records']} diagnostic records from {report['input']}")
    print(f"Gold present in returned candidates: {counts['gold_in_returned_candidates']}/{counts['diagnostic_records']}")
    print("Limitation: reranking can improve ordering inside returned candidates, but cannot recover missing candidates.")
    baseline = report["baseline"]["all"]
    tuned = report["tuned"]["all"]
    delta = report["tuned"]["delta_vs_baseline_all"]
    print(
        "Baseline all: "
        f"hit@1={baseline['hit@1']:.6f} hit@3={baseline['hit@3']:.6f} "
        f"hit@5={baseline['hit@5']:.6f} mrr@5={baseline['mrr@5']:.6f} ndcg@5={baseline['ndcg@5']:.6f}"
    )
    print(
        "Tuned all:    "
        f"hit@1={tuned['hit@1']:.6f} hit@3={tuned['hit@3']:.6f} "
        f"hit@5={tuned['hit@5']:.6f} mrr@5={tuned['mrr@5']:.6f} ndcg@5={tuned['ndcg@5']:.6f}"
    )
    print(
        "Delta:        "
        f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
        f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} ndcg@5={delta['ndcg@5']:+.6f}"
    )
    if report["baseline"]["validation"]:
        baseline_validation = report["baseline"]["validation"]
        validation = report["tuned"]["validation"]
        validation_delta = report["validation_decision"]["delta_vs_baseline"]
        print(
            "Baseline validation: "
            f"hit@1={baseline_validation['hit@1']:.6f} hit@3={baseline_validation['hit@3']:.6f} "
            f"hit@5={baseline_validation['hit@5']:.6f} mrr@5={baseline_validation['mrr@5']:.6f} "
            f"ndcg@5={baseline_validation['ndcg@5']:.6f}"
        )
        print(
            "Validation tuned: "
            f"hit@1={validation['hit@1']:.6f} hit@3={validation['hit@3']:.6f} "
            f"hit@5={validation['hit@5']:.6f} mrr@5={validation['mrr@5']:.6f} ndcg@5={validation['ndcg@5']:.6f}"
        )
        print(
            "Validation delta: "
            f"hit@1={validation_delta['hit@1']:+.6f} hit@3={validation_delta['hit@3']:+.6f} "
            f"hit@5={validation_delta['hit@5']:+.6f} mrr@5={validation_delta['mrr@5']:+.6f} "
            f"ndcg@5={validation_delta['ndcg@5']:+.6f}"
        )
        decision = report["validation_decision"]
        if decision["status"] == "pass":
            guardrail_text = ",".join(decision["guardrails"]) if decision["guardrails"] else "none"
            print(
                f"Validation decision: pass on {decision['metric']} with guardrails {guardrail_text}; "
                "candidate weights can be tested in runtime."
            )
        else:
            guardrail_text = ",".join(decision["guardrails"]) if decision["guardrails"] else "none"
            print(f"Validation decision: hold on {decision['metric']} with guardrails {guardrail_text}; keep these weights offline.")
    weights = report["tuned"]["weights"]
    if weights:
        print("Candidate nonzero weights:")
        for feature, weight in weights.items():
            print(f"  {feature}: {weight}")
    else:
        print("Candidate nonzero weights: none")
    if report["changed_cases"]:
        print("Most affected cases:")
        for item in report["changed_cases"]:
            print(
                f"  case={item['case']} row={item['dataset_row_index']} gold={item['gold']} "
                f"rank {item['baseline_rank']} -> {item['tuned_rank']}"
            )


def parse_args():
    parser = argparse.ArgumentParser(description="Tune SnapZip ranking weights from benchmark diagnostics")
    parser.add_argument("--input", required=True, type=Path, help="Benchmark JSON written by benchmarks/run.py")
    parser.add_argument("--json", type=Path, default=None, help="Optional path for the tuner report JSON")
    parser.add_argument("--metric", choices=("hit@1", "hit@3", "hit@5", "mrr@5", "ndcg@5"), default="mrr@5")
    parser.add_argument("--guardrails", default="hit@5", help="Comma-separated validation metrics that must not regress, or none")
    parser.add_argument("--passes", type=int, default=4, help="Coordinate-search passes over score features")
    parser.add_argument("--iterations", type=int, default=None, help="Alias for --passes")
    parser.add_argument("--validation-fraction", type=float, default=0.3, help="Deterministic validation split fraction")
    parser.add_argument("--features", default="", help="Comma-separated feature allowlist")
    parser.add_argument("--grid", default="", help="Comma-separated candidate weight values")
    parser.add_argument("--allow-negative", action="store_true", help="Include negative default weights for adversarial signal checks")
    parser.add_argument("--top-moves", type=int, default=10, help="Changed cases to include in the report")
    return parser.parse_args()


def main():
    args = parse_args()
    if args.iterations is not None:
        args.passes = args.iterations
    with args.input.open("r", encoding="utf-8") as handle:
        payload = json.load(handle)
    report = build_report(args, payload)
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        with args.json.open("w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2, sort_keys=True)
            handle.write("\n")
    print_report(report)


if __name__ == "__main__":
    main()
