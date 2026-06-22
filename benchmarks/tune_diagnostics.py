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
from collections import Counter
from pathlib import Path


DEFAULT_FEATURES = (
    "runtime_score",
    "runtime_rank_recip",
    "baseline_rank_recip",
    "bm25_baseline_rank_recip",
    "jaccard_baseline_rank_recip",
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
    "consensus_boost",
    "primary_fts_rank_recip",
    "query_path_rank_recip",
    "lexical_coverage_rank_recip",
    "protected_candidate",
    "external_rerank_rank_recip",
    "external_rrf_score",
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
    "language_symbol_score",
    "knowledge_card_rank_recip",
    "knowledge_card_score",
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
        records = payload.get("records")
        if isinstance(records, list):
            metadata = {
                "_run_name": payload.get("name"),
                "_run_config": payload.get("config"),
                "_run_split": payload.get("split"),
                "_run_language": payload.get("language"),
            }
            for record in records:
                if isinstance(record, dict) and "gold_snippet_index" in record and "snapzip_top5" in record:
                    enriched = dict(record)
                    for key, value in metadata.items():
                        if value is not None:
                            enriched[key] = value
                    yield enriched
            return
        if "gold_snippet_index" in payload and "snapzip_top5" in payload:
            yield payload
            return
        for value in payload.values():
            yield from iter_benchmark_records(value)
    elif isinstance(payload, list):
        for item in payload:
            yield from iter_benchmark_records(item)


def raw_features(item, rank=0):
    diagnostics = item.get("diagnostics") or {}
    benchmark_features = item.get("benchmark_features") or {}
    final_score = diagnostics.get("final_score", item.get("score", 0.0))
    features = {
        "runtime_score": -numeric(final_score),
        "runtime_rank_recip": reciprocal_rank_feature(rank or item.get("rank")),
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
        "consensus_boost": numeric(diagnostics.get("consensus_boost")),
        "primary_fts_rank_recip": reciprocal_rank_feature(diagnostics.get("primary_fts_rank")),
        "query_path_rank_recip": reciprocal_rank_feature(diagnostics.get("query_path_rank")),
        "lexical_coverage_rank_recip": reciprocal_rank_feature(diagnostics.get("lexical_coverage_rank")),
        "protected_candidate": numeric(diagnostics.get("protected_candidate")),
        "external_rerank_rank_recip": reciprocal_rank_feature(diagnostics.get("external_rerank_rank")),
        "external_rrf_score": numeric(diagnostics.get("external_rrf_score")),
        "ordered_token_overlap": numeric(diagnostics.get("ordered_token_overlap")),
        "tail_token_coverage": numeric(benchmark_features.get("tail_token_coverage")),
        "tail_ordered_overlap": numeric(benchmark_features.get("tail_ordered_overlap")),
        "last_line_token_coverage": numeric(benchmark_features.get("last_line_token_coverage")),
        "call_target_coverage": numeric(benchmark_features.get("call_target_coverage")),
        "assignment_lhs_coverage": numeric(benchmark_features.get("assignment_lhs_coverage")),
        "rare_query_token_coverage": numeric(benchmark_features.get("rare_query_token_coverage")),
        "header_token_coverage": numeric(benchmark_features.get("header_token_coverage")),
        "query_token_density": numeric(benchmark_features.get("query_token_density")),
        "candidate_shortness": numeric(benchmark_features.get("candidate_shortness")),
        "call_target_declaration_coverage": numeric(benchmark_features.get("call_target_declaration_coverage")),
        "attribute_declaration_coverage": numeric(benchmark_features.get("attribute_declaration_coverage")),
        "import_declaration_coverage": numeric(benchmark_features.get("import_declaration_coverage")),
        "tail_declaration_coverage": numeric(benchmark_features.get("tail_declaration_coverage")),
        "query_declaration_coverage": numeric(benchmark_features.get("query_declaration_coverage")),
        "query_symbol_coverage": numeric(benchmark_features.get("query_symbol_coverage")),
        "query_symbol_header_coverage": numeric(benchmark_features.get("query_symbol_header_coverage")),
        "declaration_ordered_overlap": numeric(benchmark_features.get("declaration_ordered_overlap")),
        "candidate_declaration_density": numeric(benchmark_features.get("candidate_declaration_density")),
        "language_symbol_score": numeric(diagnostics.get("language_symbol_score")),
        "knowledge_card_rank_recip": reciprocal_rank_feature(diagnostics.get("knowledge_card_rank")),
        "knowledge_card_score": numeric(diagnostics.get("knowledge_card_score")),
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
        query_intent = ""
        baseline_top = list(record.get("snapzip_top5") or [])[:5]
        baseline_ranks = {int(index): rank for rank, index in enumerate(baseline_top, start=1) if isinstance(index, int)}
        bm25_top = list(record.get("bm25_top5") or [])[:5]
        bm25_ranks = {int(index): rank for rank, index in enumerate(bm25_top, start=1) if isinstance(index, int)}
        jaccard_top = list(record.get("jaccard_top5") or [])[:5]
        jaccard_ranks = {int(index): rank for rank, index in enumerate(jaccard_top, start=1) if isinstance(index, int)}
        for fallback_rank, item in enumerate(diagnostics, start=1):
            index = candidate_index_from_path(item.get("path", ""))
            if index is None or index in seen:
                continue
            seen.add(index)
            rank = int(numeric(item.get("rank"), fallback_rank)) or fallback_rank
            item_diagnostics = item.get("diagnostics") or {}
            if not query_intent:
                query_intent = item_diagnostics.get("query_intent") or ""
            features = raw_features(item, rank)
            features["baseline_rank_recip"] = reciprocal_rank_feature(baseline_ranks.get(index))
            features["bm25_baseline_rank_recip"] = reciprocal_rank_feature(bm25_ranks.get(index))
            features["jaccard_baseline_rank_recip"] = reciprocal_rank_feature(jaccard_ranks.get(index))
            candidates.append(
                {
                    "index": index,
                    "path": item.get("path", ""),
                    "original_rank": rank,
                    "raw_features": features,
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
                "config": record.get("_run_config") or "",
                "split": record.get("_run_split") or "",
                "language": record.get("_run_language") or "",
                "query_intent": query_intent or "unknown",
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


def evaluate_gold_tops(tops):
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


def evaluate_records(records, weights=None):
    tops = []
    for record in records:
        if weights is None:
            top = baseline_top5(record)
        else:
            top = reranked_top5(record, weights)
        tops.append((record["gold"], top))
    return evaluate_gold_tops(tops)


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


def choose_initial_profile(records, feature_names, metric, guardrails):
    floor_metrics = evaluate_records(records)
    best_name = "baseline"
    best_weights = None
    best_metrics = floor_metrics
    allowed_features = set(feature_names)
    for name, weights in starter_profiles().items():
        weights = {feature: weight for feature, weight in weights.items() if feature in allowed_features}
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
    profile_name, current_weights, current_metrics = choose_initial_profile(records, feature_names, metric, guardrails)
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


def group_value(record, group_by):
    if group_by == "none":
        return ""
    if group_by == "query_intent":
        return record.get("query_intent") or "unknown"
    if group_by == "config":
        return record.get("config") or "unknown"
    if group_by == "split":
        return record.get("split") or "unknown"
    if group_by == "language":
        return record.get("language") or "unknown"
    if group_by == "config_split":
        config = record.get("config") or "unknown"
        split = record.get("split") or "unknown"
        return f"{config}/{split}"
    raise ValueError(f"unsupported group: {group_by}")


def tune_record_subset(records, feature_names, metric, guardrails, passes, grid, validation_fraction):
    train, validation = split_records(records, validation_fraction)
    tune_set = train or records
    tuned_weights, train_metrics, history = tune_weights(
        tune_set,
        feature_names,
        metric,
        guardrails,
        passes,
        grid,
    )
    baseline_all = evaluate_records(records)
    tuned_all = evaluate_records(records, tuned_weights)
    baseline_validation = evaluate_records(validation) if validation else {}
    tuned_validation = evaluate_records(validation, tuned_weights) if validation else {}
    return {
        "record_count": len(records),
        "baseline": {
            "all": baseline_all,
            "validation": baseline_validation,
        },
        "tuned": {
            "weights": clean_weights(tuned_weights),
            "train": train_metrics,
            "validation": tuned_validation,
            "all": tuned_all,
            "delta_vs_baseline_all": metrics_delta(tuned_all, baseline_all),
        },
        "validation_decision": validation_summary(metric, guardrails, baseline_validation, tuned_validation),
        "history": history,
    }


def build_group_reports(records, args, feature_names, guardrails, grid, group_by):
    if group_by == "none":
        return {}
    grouped = {}
    for record in records:
        grouped.setdefault(group_value(record, group_by), []).append(record)

    reports = {}
    for name, group_records in sorted(grouped.items()):
        if len(group_records) < args.min_group_records:
            reports[name] = {
                "record_count": len(group_records),
                "status": "skipped",
                "reason": f"fewer than --min-group-records ({args.min_group_records})",
            }
            continue
        report = tune_record_subset(
            group_records,
            feature_names,
            args.metric,
            guardrails,
            args.passes,
            grid,
            args.validation_fraction,
        )
        report["status"] = report["validation_decision"]["status"]
        reports[name] = report
    return reports


def selected_route_weights(group_reports):
    weights = {}
    for name, report in group_reports.items():
        if report.get("status") != "pass":
            continue
        tuned_weights = report.get("tuned", {}).get("weights") or {}
        if tuned_weights:
            weights[name] = tuned_weights
    return weights


def routed_top5(record, route_by, route_weights):
    weights = route_weights.get(group_value(record, route_by))
    if not weights:
        return baseline_top5(record)
    return reranked_top5(record, weights)


def evaluate_routed_records(records, route_by, route_weights):
    tops = [(record["gold"], routed_top5(record, route_by, route_weights)) for record in records]
    return evaluate_gold_tops(tops)


def changed_cases_routed(records, route_by, route_weights, limit):
    changes = []
    for record in records:
        baseline = baseline_top5(record)
        routed = routed_top5(record, route_by, route_weights)
        if baseline == routed:
            continue
        gold = record["gold"]
        before_rank = gold_rank(gold, baseline)
        after_rank = gold_rank(gold, routed)
        if before_rank == after_rank:
            continue
        changes.append(
            {
                "case": record.get("case"),
                "dataset_row_index": record.get("dataset_row_index"),
                "group": group_value(record, route_by),
                "gold": gold,
                "baseline_rank": before_rank,
                "routed_rank": after_rank,
                "baseline_top5": baseline,
                "routed_top5": routed,
            }
        )
    changes.sort(
        key=lambda item: (
            99 if item["baseline_rank"] == 0 else item["baseline_rank"],
            item["routed_rank"] if item["routed_rank"] > 0 else 99,
        )
    )
    return changes[:limit]


def build_routed_report(records, route_by, group_reports, top_moves):
    if route_by == "none":
        return {
            "enabled": False,
            "route_by": route_by,
        }
    route_weights = selected_route_weights(group_reports)
    baseline = evaluate_records(records)
    routed = evaluate_routed_records(records, route_by, route_weights)
    return {
        "enabled": True,
        "route_by": route_by,
        "selected_group_count": len(route_weights),
        "selected_weights": route_weights,
        "baseline": baseline,
        "routed": routed,
        "delta_vs_baseline": metrics_delta(routed, baseline),
        "changed_cases": changed_cases_routed(records, route_by, route_weights, top_moves),
    }


def fold_for_index(index, folds):
    return (index * 9973) % folds


def build_cross_validated_routed_report(records, args, feature_names, guardrails, grid):
    if args.route_by == "none" or args.cv_folds < 2:
        return {
            "enabled": False,
            "folds": args.cv_folds,
            "route_by": args.route_by,
        }

    baseline_tops = []
    routed_tops = []
    fold_reports = []
    selected_group_counts = Counter()
    for fold in range(args.cv_folds):
        train = []
        holdout = []
        for index, record in enumerate(records):
            if fold_for_index(index, args.cv_folds) == fold:
                holdout.append(record)
            else:
                train.append(record)
        if not train or not holdout:
            continue

        group_reports = build_group_reports(train, args, feature_names, guardrails, grid, args.route_by)
        route_weights = selected_route_weights(group_reports)
        selected_group_counts.update(route_weights.keys())

        fold_baseline_tops = [(record["gold"], baseline_top5(record)) for record in holdout]
        fold_routed_tops = [(record["gold"], routed_top5(record, args.route_by, route_weights)) for record in holdout]
        baseline_tops.extend(fold_baseline_tops)
        routed_tops.extend(fold_routed_tops)
        baseline_metrics = evaluate_gold_tops(fold_baseline_tops)
        routed_metrics = evaluate_gold_tops(fold_routed_tops)
        fold_reports.append(
            {
                "fold": fold,
                "train_records": len(train),
                "holdout_records": len(holdout),
                "selected_group_count": len(route_weights),
                "selected_groups": sorted(route_weights),
                "baseline": baseline_metrics,
                "routed": routed_metrics,
                "delta_vs_baseline": metrics_delta(routed_metrics, baseline_metrics),
            }
        )

    baseline = evaluate_gold_tops(baseline_tops)
    routed = evaluate_gold_tops(routed_tops)
    delta = metrics_delta(routed, baseline)
    guardrail_passes = all(delta.get(metric_name, 0.0) >= 0 for metric_name in guardrails)
    metric_improves = delta.get(args.metric, 0.0) > 0
    neutral = guardrail_passes and delta.get(args.metric, 0.0) == 0
    required_metrics = (args.metric, *guardrails)
    fold_failures = []
    for fold in fold_reports:
        fold_delta = fold["delta_vs_baseline"]
        failing_metrics = [metric_name for metric_name in required_metrics if fold_delta.get(metric_name, 0.0) < 0]
        if failing_metrics:
            fold_failures.append(
                {
                    "fold": fold["fold"],
                    "failing_metrics": failing_metrics,
                    "delta_vs_baseline": fold_delta,
                }
            )
    fold_guardrails_pass = not fold_failures
    return {
        "enabled": True,
        "folds": args.cv_folds,
        "route_by": args.route_by,
        "evaluated_records": len(baseline_tops),
        "selected_group_counts": dict(sorted(selected_group_counts.items())),
        "baseline": baseline,
        "routed": routed,
        "delta_vs_baseline": delta,
        "decision": {
            "status": "pass" if metric_improves and guardrail_passes and fold_guardrails_pass else "neutral" if neutral and fold_guardrails_pass else "hold",
            "metric": args.metric,
            "guardrails": list(guardrails),
            "fold_guardrails_pass": fold_guardrails_pass,
            "fold_failures": fold_failures,
        },
        "fold_reports": fold_reports,
    }


def build_cross_validated_tuned_report(records, args, feature_names, guardrails, grid):
    if args.cv_folds < 2:
        return {
            "enabled": False,
            "folds": args.cv_folds,
        }

    baseline_tops = []
    tuned_tops = []
    fold_reports = []
    selected_weight_counts = Counter()
    for fold in range(args.cv_folds):
        train = []
        holdout = []
        for index, record in enumerate(records):
            if fold_for_index(index, args.cv_folds) == fold:
                holdout.append(record)
            else:
                train.append(record)
        if not train or not holdout:
            continue

        selection_train, selection_validation = split_records(train, args.cv_selection_validation_fraction)
        tune_train = selection_train or train
        tuned_weights, train_metrics, history = tune_weights(
            tune_train,
            feature_names,
            args.metric,
            guardrails,
            args.passes,
            grid,
        )
        selection_baseline = evaluate_records(selection_validation) if selection_validation else {}
        selection_tuned = evaluate_records(selection_validation, tuned_weights) if selection_validation else {}
        selection_decision = validation_summary(args.metric, guardrails, selection_baseline, selection_tuned)
        applied_weights = tuned_weights
        if selection_validation and selection_decision["status"] != "pass":
            applied_weights = None
        cleaned_weights = clean_weights(tuned_weights)
        applied_cleaned_weights = clean_weights(applied_weights)
        selected_weight_counts.update(applied_cleaned_weights.keys())
        fold_baseline_tops = [(record["gold"], baseline_top5(record)) for record in holdout]
        fold_tuned_tops = [
            (record["gold"], reranked_top5(record, applied_weights) if applied_weights is not None else baseline_top5(record))
            for record in holdout
        ]
        baseline_tops.extend(fold_baseline_tops)
        tuned_tops.extend(fold_tuned_tops)
        baseline_metrics = evaluate_gold_tops(fold_baseline_tops)
        tuned_metrics = evaluate_gold_tops(fold_tuned_tops)
        fold_reports.append(
            {
                "fold": fold,
                "train_records": len(train),
                "holdout_records": len(holdout),
                "selection_train_records": len(tune_train),
                "selection_validation_records": len(selection_validation),
                "selection_validation": {
                    "baseline": selection_baseline,
                    "tuned": selection_tuned,
                    "decision": selection_decision,
                },
                "applied": applied_weights is not None,
                "train": train_metrics,
                "baseline": baseline_metrics,
                "tuned": tuned_metrics,
                "delta_vs_baseline": metrics_delta(tuned_metrics, baseline_metrics),
                "weights": cleaned_weights,
                "applied_weights": applied_cleaned_weights,
                "history": history,
            }
        )

    baseline = evaluate_gold_tops(baseline_tops)
    tuned = evaluate_gold_tops(tuned_tops)
    delta = metrics_delta(tuned, baseline)
    guardrail_passes = all(delta.get(metric_name, 0.0) >= 0 for metric_name in guardrails)
    metric_improves = delta.get(args.metric, 0.0) > 0
    neutral = guardrail_passes and delta.get(args.metric, 0.0) == 0
    required_metrics = (args.metric, *guardrails)
    fold_failures = []
    for fold in fold_reports:
        fold_delta = fold["delta_vs_baseline"]
        failing_metrics = [metric_name for metric_name in required_metrics if fold_delta.get(metric_name, 0.0) < 0]
        if failing_metrics:
            fold_failures.append(
                {
                    "fold": fold["fold"],
                    "failing_metrics": failing_metrics,
                    "delta_vs_baseline": fold_delta,
                }
            )
    fold_guardrails_pass = not fold_failures
    return {
        "enabled": True,
        "folds": args.cv_folds,
        "evaluated_records": len(baseline_tops),
        "baseline": baseline,
        "tuned": tuned,
        "delta_vs_baseline": delta,
        "decision": {
            "status": "pass" if metric_improves and guardrail_passes and fold_guardrails_pass else "neutral" if neutral and fold_guardrails_pass else "hold",
            "metric": args.metric,
            "guardrails": list(guardrails),
            "fold_guardrails_pass": fold_guardrails_pass,
            "fold_failures": fold_failures,
        },
        "selected_weight_counts": dict(sorted(selected_weight_counts.items())),
        "fold_reports": fold_reports,
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

    grid = [float(value) for value in args.grid.split(",")] if args.grid else (NEGATIVE_GRID + DEFAULT_GRID if args.allow_negative else DEFAULT_GRID)
    train, validation = split_records(prepared, args.validation_fraction)
    tune_set = train or prepared
    tuned_weights, train_metrics, history = tune_weights(
        tune_set,
        feature_names,
        args.metric,
        guardrails,
        args.passes,
        grid,
    )

    baseline_all = evaluate_records(prepared)
    tuned_all = evaluate_records(prepared, tuned_weights)
    baseline_train = evaluate_records(train) if train else {}
    baseline_validation = evaluate_records(validation) if validation else {}
    tuned_validation = evaluate_records(validation, tuned_weights) if validation else {}
    validation_decision = validation_summary(args.metric, guardrails, baseline_validation, tuned_validation)

    gold_in_candidates = sum(1 for record in prepared if record["gold"] in [candidate["index"] for candidate in record["candidates"]])
    group_reports = build_group_reports(prepared, args, feature_names, guardrails, grid, args.group_by)
    if args.route_by == "none":
        route_group_reports = {}
    elif args.route_by == args.group_by:
        route_group_reports = group_reports
    else:
        route_group_reports = build_group_reports(prepared, args, feature_names, guardrails, grid, args.route_by)
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
        "groups": {
            "group_by": args.group_by,
            "min_group_records": args.min_group_records,
            "reports": group_reports,
        },
        "cross_validated_tuned": build_cross_validated_tuned_report(prepared, args, feature_names, guardrails, grid),
        "routed": build_routed_report(prepared, args.route_by, route_group_reports, args.top_moves),
        "cross_validated_routed": build_cross_validated_routed_report(prepared, args, feature_names, guardrails, grid),
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
    group_reports = (report.get("groups") or {}).get("reports") or {}
    if group_reports:
        group_by = report["groups"]["group_by"]
        print(f"Grouped tuning by {group_by}:")
        for name, group in group_reports.items():
            if group.get("status") == "skipped":
                print(f"  {name}: skipped ({group['record_count']} records)")
                continue
            delta = group["tuned"]["delta_vs_baseline_all"]
            validation_delta = group["validation_decision"].get("delta_vs_baseline") or {}
            weights = group["tuned"]["weights"]
            print(
                f"  {name}: status={group['status']} records={group['record_count']} "
                f"all_hit@5={delta['hit@5']:+.6f} all_mrr@5={delta['mrr@5']:+.6f} "
                f"validation_hit@5={validation_delta.get('hit@5', 0.0):+.6f} "
                f"validation_mrr@5={validation_delta.get('mrr@5', 0.0):+.6f} "
                f"weights={weights if weights else '{}'}"
            )
    routed = report.get("routed") or {}
    if routed.get("enabled"):
        delta = routed["delta_vs_baseline"]
        print(
            f"Routed evaluation by {routed['route_by']}: "
            f"selected_groups={routed['selected_group_count']} "
            f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
            f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} "
            f"ndcg@5={delta['ndcg@5']:+.6f}"
        )
        if routed["selected_weights"]:
            print("Selected route weights:")
            for group, weights in routed["selected_weights"].items():
                print(f"  {group}: {weights}")
        if routed["changed_cases"]:
            print("Most affected routed cases:")
            for item in routed["changed_cases"]:
                print(
                    f"  group={item['group']} case={item['case']} row={item['dataset_row_index']} "
                    f"gold={item['gold']} rank {item['baseline_rank']} -> {item['routed_rank']}"
                )
    cv_routed = report.get("cross_validated_routed") or {}
    if cv_routed.get("enabled"):
        delta = cv_routed["delta_vs_baseline"]
        decision = cv_routed.get("decision") or {}
        print(
            f"Cross-validated routed evaluation by {cv_routed['route_by']} "
            f"({cv_routed['folds']} folds): "
            f"status={decision.get('status', 'unknown')} "
            f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
            f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} "
            f"ndcg@5={delta['ndcg@5']:+.6f}"
        )
        if decision.get("fold_failures"):
            print(f"Cross-validated routed fold failures: {len(decision['fold_failures'])}")
        if cv_routed["selected_group_counts"]:
            print(f"Cross-validated selected groups: {cv_routed['selected_group_counts']}")
    cv_tuned = report.get("cross_validated_tuned") or {}
    if cv_tuned.get("enabled"):
        delta = cv_tuned["delta_vs_baseline"]
        decision = cv_tuned["decision"]
        print(
            f"Cross-validated global tuning ({cv_tuned['folds']} folds): "
            f"status={decision['status']} "
            f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
            f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} "
            f"ndcg@5={delta['ndcg@5']:+.6f}"
        )
        if decision.get("fold_failures"):
            print(f"Cross-validated fold failures: {len(decision['fold_failures'])}")
        if cv_tuned["selected_weight_counts"]:
            print(f"Cross-validated selected weights: {cv_tuned['selected_weight_counts']}")


def parse_args():
    parser = argparse.ArgumentParser(description="Tune SnapZip ranking weights from benchmark diagnostics")
    parser.add_argument("--input", required=True, type=Path, help="Benchmark JSON written by benchmarks/run.py")
    parser.add_argument("--json", type=Path, default=None, help="Optional path for the tuner report JSON")
    parser.add_argument("--metric", choices=("hit@1", "hit@3", "hit@5", "mrr@5", "ndcg@5"), default="mrr@5")
    parser.add_argument("--guardrails", default="hit@5", help="Comma-separated validation metrics that must not regress, or none")
    parser.add_argument("--passes", type=int, default=4, help="Coordinate-search passes over score features")
    parser.add_argument("--iterations", type=int, default=None, help="Alias for --passes")
    parser.add_argument("--validation-fraction", type=float, default=0.3, help="Deterministic validation split fraction")
    parser.add_argument(
        "--cv-selection-validation-fraction",
        type=float,
        default=0.0,
        help="Optional inner validation split for cross-validated global tuning; rejects fold weights that fail validation",
    )
    parser.add_argument("--features", default="", help="Comma-separated feature allowlist")
    parser.add_argument("--grid", default="", help="Comma-separated candidate weight values")
    parser.add_argument("--allow-negative", action="store_true", help="Include negative default weights for adversarial signal checks")
    parser.add_argument(
        "--group-by",
        choices=("none", "query_intent", "config", "split", "language", "config_split"),
        default="none",
        help="Also tune independent offline profiles for each group",
    )
    parser.add_argument(
        "--route-by",
        choices=("none", "query_intent", "config", "split", "language", "config_split"),
        default="none",
        help="Offline routed evaluation: apply validation-passing group profiles and baseline elsewhere",
    )
    parser.add_argument("--cv-folds", type=int, default=0, help="Cross-validated global/routed evaluation folds")
    parser.add_argument("--min-group-records", type=int, default=20, help="Minimum records required to tune a group")
    parser.add_argument("--top-moves", type=int, default=10, help="Changed cases to include in the report")
    return parser.parse_args()


def main():
    args = parse_args()
    if args.iterations is not None:
        args.passes = args.iterations
    if args.cv_folds < 0:
        raise SystemExit("--cv-folds must be zero or greater")
    if args.cv_selection_validation_fraction < 0 or args.cv_selection_validation_fraction >= 1:
        raise SystemExit("--cv-selection-validation-fraction must be at least 0 and less than 1")
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
