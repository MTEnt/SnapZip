#!/usr/bin/env python3
"""Test bounded candidate-promotion policies from SnapZip diagnostics.

This script consumes RepoBench JSON produced with --snapzip-diagnostics and
tests a safer alternative to full reranking: promote at most one lower-ranked
candidate into one bounded top-5 slot when strong diagnostic signals agree.

It is offline-only. A policy should not move into runtime retrieval unless the
cross-validated selected-policy decision is "pass" with the requested
guardrails.
"""

import argparse
import json
from collections import Counter
from pathlib import Path

from tune_diagnostics import (
    baseline_top5,
    clean_weights,
    evaluate_gold_tops,
    fold_for_index,
    gold_rank,
    iter_benchmark_records,
    metrics_delta,
    parse_metric_list,
    prepare_records,
)


PROMOTION_FEATURES = (
    "bm25_baseline_rank_recip",
    "jaccard_baseline_rank_recip",
    "consensus_boost",
    "structured_path_boost",
    "ordered_token_overlap",
    "assignment_lhs_coverage",
    "header_token_coverage",
    "rare_query_token_coverage",
    "call_target_coverage",
    "tail_token_coverage",
    "tail_ordered_overlap",
    "last_line_token_coverage",
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
)

PROMOTION_PROFILES = {
    "content": {
        "rare_query_token_coverage": 0.60,
        "tail_token_coverage": 0.45,
        "tail_ordered_overlap": 0.35,
        "last_line_token_coverage": 0.25,
        "ordered_token_overlap": 0.25,
        "header_token_coverage": 0.25,
        "assignment_lhs_coverage": 0.15,
        "call_target_coverage": 0.15,
    },
    "content_consensus": {
        "rare_query_token_coverage": 0.45,
        "tail_token_coverage": 0.35,
        "tail_ordered_overlap": 0.25,
        "last_line_token_coverage": 0.20,
        "ordered_token_overlap": 0.20,
        "header_token_coverage": 0.20,
        "assignment_lhs_coverage": 0.10,
        "call_target_coverage": 0.10,
        "consensus_boost": 0.35,
        "structured_path_boost": 0.20,
    },
    "baseline_rescue": {
        "bm25_baseline_rank_recip": 0.45,
        "jaccard_baseline_rank_recip": 0.45,
        "rare_query_token_coverage": 0.35,
        "tail_token_coverage": 0.25,
        "header_token_coverage": 0.20,
        "ordered_token_overlap": 0.15,
    },
    "assignment": {
        "assignment_lhs_coverage": 0.70,
        "rare_query_token_coverage": 0.35,
        "tail_token_coverage": 0.25,
        "header_token_coverage": 0.20,
        "ordered_token_overlap": 0.20,
    },
    "call": {
        "call_target_coverage": 0.70,
        "call_target_declaration_coverage": 0.45,
        "rare_query_token_coverage": 0.30,
        "tail_ordered_overlap": 0.25,
        "ordered_token_overlap": 0.25,
        "consensus_boost": 0.20,
    },
    "symbol_consensus": {
        "query_symbol_header_coverage": 0.55,
        "query_declaration_coverage": 0.40,
        "tail_declaration_coverage": 0.30,
        "declaration_ordered_overlap": 0.30,
        "rare_query_token_coverage": 0.25,
        "consensus_boost": 0.25,
        "structured_path_boost": 0.15,
    },
    "call_declaration": {
        "call_target_declaration_coverage": 0.80,
        "query_symbol_header_coverage": 0.35,
        "tail_declaration_coverage": 0.25,
        "rare_query_token_coverage": 0.25,
        "consensus_boost": 0.20,
    },
    "import_declaration": {
        "import_declaration_coverage": 0.80,
        "query_symbol_header_coverage": 0.35,
        "query_declaration_coverage": 0.30,
        "rare_query_token_coverage": 0.20,
        "structured_path_boost": 0.20,
    },
    "knowledge_card_consensus": {
        "knowledge_card_rank_recip": 0.55,
        "knowledge_card_score": 0.35,
        "rare_query_token_coverage": 0.25,
        "query_symbol_header_coverage": 0.25,
        "query_declaration_coverage": 0.20,
        "consensus_boost": 0.20,
        "structured_path_boost": 0.15,
    },
}


def parse_csv_ints(value):
    return tuple(int(item.strip()) for item in value.split(",") if item.strip())


def parse_csv_floats(value):
    return tuple(float(item.strip()) for item in value.split(",") if item.strip())


def parse_profile_names(value):
    if not value:
        return tuple(PROMOTION_PROFILES)
    names = tuple(item.strip() for item in value.split(",") if item.strip())
    unknown = [name for name in names if name not in PROMOTION_PROFILES]
    if unknown:
        raise SystemExit(f"unknown promotion profile(s): {', '.join(unknown)}")
    return names


def policy_grid(args):
    policies = []
    profile_names = parse_profile_names(args.profiles)
    for profile in profile_names:
        for max_rank in parse_csv_ints(args.max_ranks):
            for margin in parse_csv_floats(args.margins):
                for min_score in parse_csv_floats(args.min_scores):
                    for slot in parse_csv_ints(args.slots):
                        if slot < 1 or slot > 5:
                            raise SystemExit("--slots values must be between 1 and 5")
                        require_values = (True,) if args.require_external_baseline else (False, True)
                        for require_external_baseline in require_values:
                            policies.append(
                                {
                                    "profile": profile,
                                    "max_rank": max_rank,
                                    "margin": margin,
                                    "min_score": min_score,
                                    "slot": slot,
                                    "require_external_baseline": require_external_baseline,
                                }
                            )
    return policies


def profile_score(candidate, profile):
    weights = PROMOTION_PROFILES[profile]
    return sum(candidate["features"].get(feature, 0.0) * weight for feature, weight in weights.items())


def has_external_baseline_support(candidate):
    raw = candidate.get("raw_features") or {}
    return raw.get("bm25_baseline_rank_recip", 0.0) > 0 or raw.get("jaccard_baseline_rank_recip", 0.0) > 0


def promoted_top5(record, policy):
    top = baseline_top5(record)
    if policy is None or len(top) < 5:
        return top

    slot = policy["slot"]
    profile = policy["profile"]
    by_index = {candidate["index"]: candidate for candidate in record["candidates"]}
    replacement = by_index.get(top[slot - 1])
    replacement_score = profile_score(replacement, profile) if replacement else 0.0

    eligible = []
    for candidate in record["candidates"]:
        if candidate["index"] in top:
            continue
        if candidate["original_rank"] > policy["max_rank"]:
            continue
        if policy["require_external_baseline"] and not has_external_baseline_support(candidate):
            continue

        score = profile_score(candidate, profile)
        if score < policy["min_score"]:
            continue
        if score - replacement_score < policy["margin"]:
            continue
        eligible.append((score, candidate["original_rank"], candidate["index"]))

    if not eligible:
        return top

    eligible.sort(key=lambda item: (-item[0], item[1], item[2]))
    promoted = eligible[0][2]
    changed = list(top)
    changed[slot - 1] = promoted

    # Preserve baseline ordering and avoid duplicates if the replacement also
    # appears elsewhere due malformed diagnostic output.
    deduped = []
    for index in changed + top:
        if index not in deduped:
            deduped.append(index)
        if len(deduped) >= 5:
            break
    return deduped


def evaluate_policy(records, policy):
    return evaluate_gold_tops([(record["gold"], promoted_top5(record, policy)) for record in records])


def policy_identity(policy):
    if policy is None:
        return None
    return {
        "profile": policy["profile"],
        "weights": clean_weights(PROMOTION_PROFILES[policy["profile"]]),
        "max_rank": policy["max_rank"],
        "margin": policy["margin"],
        "min_score": policy["min_score"],
        "slot": policy["slot"],
        "require_external_baseline": policy["require_external_baseline"],
    }


def acceptable_delta(delta, required_metrics):
    return all(delta.get(metric, 0.0) >= 0 for metric in required_metrics)


def candidate_policy_key(delta, metric, guardrails):
    return (
        delta.get(metric, 0.0),
        delta.get("hit@5", 0.0),
        delta.get("mrr@5", 0.0),
        delta.get("ndcg@5", 0.0),
        delta.get("hit@3", 0.0),
        delta.get("hit@1", 0.0),
        *(delta.get(guardrail, 0.0) for guardrail in guardrails),
    )


def select_policy(records, policies, metric, guardrails):
    baseline = evaluate_policy(records, None)
    required = (metric, *guardrails)
    best_policy = None
    best_delta = None
    best_key = None
    for policy in policies:
        candidate = evaluate_policy(records, policy)
        delta = metrics_delta(candidate, baseline)
        if delta.get(metric, 0.0) <= 0:
            continue
        if not acceptable_delta(delta, required):
            continue
        key = candidate_policy_key(delta, metric, guardrails)
        if best_key is None or key > best_key:
            best_policy = policy
            best_delta = delta
            best_key = key
    return best_policy, best_delta or {}


def changed_cases(records, policy, limit):
    changes = []
    counts = Counter()
    for record in records:
        baseline = baseline_top5(record)
        promoted = promoted_top5(record, policy)
        gold = record["gold"]
        before = gold_rank(gold, baseline)
        after = gold_rank(gold, promoted)
        if before == 0 and after > 0:
            counts["recovered_top5"] += 1
        elif before > 0 and after == 0:
            counts["lost_top5"] += 1
        elif after > 0 and (before == 0 or after < before):
            counts["improved_rank"] += 1
        elif before > 0 and after > before:
            counts["worsened_rank"] += 1
        if baseline == promoted or before == after:
            continue
        changes.append(
            {
                "case": record.get("case"),
                "dataset_row_index": record.get("dataset_row_index"),
                "config": record.get("config"),
                "split": record.get("split"),
                "language": record.get("language"),
                "query_intent": record.get("query_intent"),
                "gold": gold,
                "baseline_rank": before,
                "promoted_rank": after,
                "baseline_top5": baseline,
                "promoted_top5": promoted,
            }
        )
    changes.sort(
        key=lambda item: (
            item["promoted_rank"] == 0,
            item["promoted_rank"] if item["promoted_rank"] > 0 else 99,
            99 if item["baseline_rank"] == 0 else item["baseline_rank"],
        )
    )
    return dict(sorted(counts.items())), changes[:limit]


def fixed_policy_report(records, policies, metric, guardrails, folds, limit):
    baseline = evaluate_policy(records, None)
    required = (metric, *guardrails)
    candidates = []
    for policy in policies:
        promoted = evaluate_policy(records, policy)
        delta = metrics_delta(promoted, baseline)
        if delta.get(metric, 0.0) <= 0:
            continue
        fold_failures = []
        if folds >= 2:
            for fold in range(folds):
                holdout = [record for index, record in enumerate(records) if fold_for_index(index, folds) == fold]
                if not holdout:
                    continue
                fold_baseline = evaluate_policy(holdout, None)
                fold_promoted = evaluate_policy(holdout, policy)
                fold_delta = metrics_delta(fold_promoted, fold_baseline)
                failing_metrics = [name for name in required if fold_delta.get(name, 0.0) < 0]
                if failing_metrics:
                    fold_failures.append(
                        {
                            "fold": fold,
                            "failing_metrics": failing_metrics,
                            "delta_vs_baseline": fold_delta,
                        }
                    )
        counts, changes = changed_cases(records, policy, limit)
        candidates.append(
            {
                "policy": policy_identity(policy),
                "promoted": promoted,
                "delta_vs_baseline": delta,
                "fold_failures": fold_failures,
                "outcome_counts": counts,
                "changed_cases": changes,
            }
        )
    candidates.sort(
        key=lambda item: (
            not item["fold_failures"],
            item["delta_vs_baseline"].get(metric, 0.0),
            item["delta_vs_baseline"].get("hit@5", 0.0),
            item["delta_vs_baseline"].get("mrr@5", 0.0),
        ),
        reverse=True,
    )
    return {
        "baseline": baseline,
        "top_candidates": candidates[:limit],
    }


def cross_validated_report(records, policies, metric, guardrails, folds, top_moves):
    if folds < 2:
        return {
            "enabled": False,
            "folds": folds,
        }

    required = (metric, *guardrails)
    baseline_tops = []
    promoted_tops = []
    fold_reports = []
    selected_counts = Counter()

    for fold in range(folds):
        train = []
        holdout = []
        for index, record in enumerate(records):
            if fold_for_index(index, folds) == fold:
                holdout.append(record)
            else:
                train.append(record)
        if not train or not holdout:
            continue

        selected, train_delta = select_policy(train, policies, metric, guardrails)
        if selected is not None:
            selected_counts.update([selected["profile"]])

        fold_baseline_tops = [(record["gold"], baseline_top5(record)) for record in holdout]
        fold_promoted_tops = [(record["gold"], promoted_top5(record, selected)) for record in holdout]
        baseline_tops.extend(fold_baseline_tops)
        promoted_tops.extend(fold_promoted_tops)

        baseline_metrics = evaluate_gold_tops(fold_baseline_tops)
        promoted_metrics = evaluate_gold_tops(fold_promoted_tops)
        fold_reports.append(
            {
                "fold": fold,
                "train_records": len(train),
                "holdout_records": len(holdout),
                "selected_policy": policy_identity(selected),
                "train_delta_vs_baseline": train_delta,
                "baseline": baseline_metrics,
                "promoted": promoted_metrics,
                "delta_vs_baseline": metrics_delta(promoted_metrics, baseline_metrics),
            }
        )

    baseline = evaluate_gold_tops(baseline_tops)
    promoted = evaluate_gold_tops(promoted_tops)
    delta = metrics_delta(promoted, baseline)
    fold_failures = []
    for fold in fold_reports:
        failing_metrics = [metric_name for metric_name in required if fold["delta_vs_baseline"].get(metric_name, 0.0) < 0]
        if failing_metrics:
            fold_failures.append(
                {
                    "fold": fold["fold"],
                    "failing_metrics": failing_metrics,
                    "delta_vs_baseline": fold["delta_vs_baseline"],
                }
            )

    aggregate_guardrails_pass = acceptable_delta(delta, guardrails)
    metric_improves = delta.get(metric, 0.0) > 0
    neutral = aggregate_guardrails_pass and delta.get(metric, 0.0) == 0
    no_fold_failures = not fold_failures
    status = "pass" if metric_improves and aggregate_guardrails_pass and no_fold_failures else "neutral" if neutral and no_fold_failures else "hold"
    return {
        "enabled": True,
        "folds": folds,
        "baseline": baseline,
        "promoted": promoted,
        "delta_vs_baseline": delta,
        "decision": {
            "status": status,
            "metric": metric,
            "guardrails": list(guardrails),
            "fold_guardrails_pass": no_fold_failures,
            "fold_failures": fold_failures,
        },
        "selected_profile_counts": dict(sorted(selected_counts.items())),
        "fold_reports": fold_reports,
    }


def build_report(args, payload):
    features = tuple(feature.strip() for feature in args.features.split(",") if feature.strip()) if args.features else PROMOTION_FEATURES
    raw_records = list(iter_benchmark_records(payload))
    records, skipped = prepare_records(raw_records, features)
    if not records:
        raise SystemExit("no benchmark records with snapzip_diagnostics were found")

    policies = policy_grid(args)
    guardrails = parse_metric_list(args.guardrails)
    fixed = fixed_policy_report(records, policies, args.metric, guardrails, args.cv_folds, args.top_policies)
    cv = cross_validated_report(records, policies, args.metric, guardrails, args.cv_folds, args.top_moves)
    return {
        "input": str(args.input),
        "metric": args.metric,
        "guardrails": list(guardrails),
        "features": list(features),
        "policy_count": len(policies),
        "record_counts": {
            "raw_records": len(raw_records),
            "diagnostic_records": len(records),
            **skipped,
        },
        "limitation": "Promotion only reorders SnapZip's returned diagnostic candidates; it cannot recover candidates missing from the diagnostic set.",
        "fixed_policy_candidates": fixed,
        "cross_validated_selection": cv,
    }


def print_metrics(label, metrics):
    print(
        f"{label}: "
        f"hit@1={metrics['hit@1']:.6f} hit@3={metrics['hit@3']:.6f} "
        f"hit@5={metrics['hit@5']:.6f} mrr@5={metrics['mrr@5']:.6f} ndcg@5={metrics['ndcg@5']:.6f}"
    )


def print_delta(label, delta):
    print(
        f"{label}: "
        f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
        f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} ndcg@5={delta['ndcg@5']:+.6f}"
    )


def print_report(report):
    counts = report["record_counts"]
    print(f"Loaded {counts['diagnostic_records']} diagnostic records from {report['input']}")
    print(f"Tested {report['policy_count']} bounded promotion policies")
    print("Limitation: promotion can only reorder returned diagnostic candidates.")
    fixed = report["fixed_policy_candidates"]
    print_metrics("Baseline", fixed["baseline"])
    if fixed["top_candidates"]:
        print("Top fixed policy candidates:")
        for item in fixed["top_candidates"]:
            policy = item["policy"]
            delta = item["delta_vs_baseline"]
            failure_text = f"fold_failures={len(item['fold_failures'])}"
            print(
                f"  {policy['profile']} slot={policy['slot']} max_rank={policy['max_rank']} "
                f"margin={policy['margin']} min_score={policy['min_score']} "
                f"external={policy['require_external_baseline']} {failure_text} "
                f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f}"
            )
    cv = report["cross_validated_selection"]
    if cv.get("enabled"):
        print_metrics(f"Cross-validated baseline ({cv['folds']} folds)", cv["baseline"])
        print_metrics("Cross-validated promoted", cv["promoted"])
        print_delta("Cross-validated delta", cv["delta_vs_baseline"])
        decision = cv["decision"]
        print(
            f"Cross-validated decision: {decision['status']} "
            f"on {decision['metric']} with guardrails {','.join(decision['guardrails']) or 'none'}"
        )
        if decision["fold_failures"]:
            print(f"Cross-validated fold failures: {len(decision['fold_failures'])}")
        if cv["selected_profile_counts"]:
            print(f"Selected profile counts: {cv['selected_profile_counts']}")


def parse_args():
    parser = argparse.ArgumentParser(description="Test bounded SnapZip diagnostic candidate-promotion policies")
    parser.add_argument("--input", required=True, type=Path, help="Benchmark JSON written by benchmarks/run.py")
    parser.add_argument("--json", type=Path, default=None, help="Optional path for the promotion report JSON")
    parser.add_argument("--metric", choices=("hit@1", "hit@3", "hit@5", "mrr@5", "ndcg@5"), default="mrr@5")
    parser.add_argument("--guardrails", default="hit@5", help="Comma-separated metrics that must not regress, or none")
    parser.add_argument("--features", default="", help="Comma-separated feature allowlist")
    parser.add_argument("--profiles", default="", help="Comma-separated promotion profile names")
    parser.add_argument("--max-ranks", default="7,10,15,20", help="Comma-separated max diagnostic ranks eligible for promotion")
    parser.add_argument("--margins", default="-0.2,0,0.2", help="Comma-separated score margins over the replaced slot")
    parser.add_argument("--min-scores", default="0,0.4,0.55", help="Comma-separated minimum promotion scores")
    parser.add_argument("--slots", default="5,3", help="Comma-separated one-based top-5 slots that may be replaced")
    parser.add_argument("--require-external-baseline", action="store_true", help="Only test policies requiring BM25 or Jaccard top-5 support")
    parser.add_argument("--cv-folds", type=int, default=5, help="Deterministic cross-validation folds")
    parser.add_argument("--top-policies", type=int, default=8, help="Fixed policy candidates to include")
    parser.add_argument("--top-moves", type=int, default=10, help="Changed cases to include")
    return parser.parse_args()


def main():
    args = parse_args()
    if args.cv_folds < 0:
        raise SystemExit("--cv-folds must be zero or greater")
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
