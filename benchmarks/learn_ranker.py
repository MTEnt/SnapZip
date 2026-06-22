#!/usr/bin/env python3
"""Train and validate an offline SnapZip learning-to-rank model.

This script consumes RepoBench JSON produced with --snapzip-diagnostics. It
learns a small linear pairwise ranker over existing SnapZip diagnostic features
and evaluates it with deterministic cross-validation. It is intentionally
offline-only: a model should not be moved into runtime retrieval unless the
held-out report beats the current ranking with the requested guardrails.
"""

import argparse
import json
import random
from collections import Counter
from pathlib import Path

from tune_diagnostics import (
    DEFAULT_FEATURES,
    baseline_top5,
    clean_weights,
    evaluate_gold_tops,
    evaluate_records,
    fold_for_index,
    gold_rank,
    iter_benchmark_records,
    metrics_delta,
    parse_metric_list,
    prepare_records,
    reranked_top5,
    score_candidate,
)


def initial_weights(feature_names, profile):
    if profile == "zero":
        return {}
    if profile == "baseline":
        if "baseline_rank_recip" in feature_names:
            return {"baseline_rank_recip": 1.0}
        return {}
    if profile == "runtime":
        weights = {}
        if "runtime_score" in feature_names:
            weights["runtime_score"] = 1.0
        if "runtime_rank_recip" in feature_names:
            weights["runtime_rank_recip"] = 0.25
        return weights
    raise ValueError(f"unknown initial profile: {profile}")


def find_gold_candidate(record):
    gold = record["gold"]
    for candidate in record["candidates"]:
        if candidate["index"] == gold:
            return candidate
    return None


def selected_negatives(record, gold_candidate, weights, strategy, max_negatives):
    negatives = [candidate for candidate in record["candidates"] if candidate["index"] != gold_candidate["index"]]
    if strategy == "all":
        ranked = negatives
    elif strategy == "hard":
        ranked = sorted(
            negatives,
            key=lambda candidate: (-score_candidate(candidate, weights), candidate["original_rank"], candidate["index"]),
        )
    else:
        raise ValueError(f"unknown negative strategy: {strategy}")
    if max_negatives <= 0:
        return ranked
    return ranked[:max_negatives]


def shrink_weights(weights, feature_names, learning_rate, l2):
    if l2 <= 0:
        return
    decay = max(0.0, 1.0 - learning_rate * l2)
    for feature in feature_names:
        if feature in weights:
            weights[feature] *= decay


def clamp_weights(weights, max_abs_weight, protected=()):
    if max_abs_weight <= 0:
        return
    protected = set(protected)
    for feature, value in list(weights.items()):
        if feature in protected:
            continue
        if value > max_abs_weight:
            weights[feature] = max_abs_weight
        elif value < -max_abs_weight:
            weights[feature] = -max_abs_weight
        elif abs(value) < 1e-12:
            del weights[feature]


def frozen_features(args):
    if not args.freeze_anchor:
        return set()
    return {"baseline_rank_recip", "runtime_score", "runtime_rank_recip"}


def update_pair(weights, feature_names, gold_candidate, negative_candidate, args):
    margin = score_candidate(gold_candidate, weights) - score_candidate(negative_candidate, weights)
    if margin >= args.margin:
        return False
    frozen = frozen_features(args)
    trainable_features = tuple(feature for feature in feature_names if feature not in frozen)
    shrink_weights(weights, trainable_features, args.learning_rate, args.l2)
    for feature in feature_names:
        if feature in frozen:
            continue
        delta = gold_candidate["features"].get(feature, 0.0) - negative_candidate["features"].get(feature, 0.0)
        if abs(delta) <= 1e-12:
            continue
        weights[feature] = weights.get(feature, 0.0) + args.learning_rate * delta
    clamp_weights(weights, args.max_abs_weight, frozen)
    return True


def trainable_records(records):
    return [record for record in records if find_gold_candidate(record) is not None]


def train_ranker(records, feature_names, args):
    weights = initial_weights(feature_names, args.initial_profile)
    usable = trainable_records(records)
    rng = random.Random(args.seed)
    history = []
    if not usable:
        return weights, {"trainable_records": 0, "epochs": history}

    for epoch in range(1, args.epochs + 1):
        ordered = list(usable)
        rng.shuffle(ordered)
        updates = 0
        pairs = 0
        for record in ordered:
            gold_candidate = find_gold_candidate(record)
            if gold_candidate is None:
                continue
            negatives = selected_negatives(record, gold_candidate, weights, args.negative_strategy, args.max_negatives)
            for negative_candidate in negatives:
                pairs += 1
                if update_pair(weights, feature_names, gold_candidate, negative_candidate, args):
                    updates += 1
        if epoch == 1 or epoch == args.epochs or epoch % args.history_every == 0:
            metrics = evaluate_records(records, weights)
            history.append(
                {
                    "epoch": epoch,
                    "pairs": pairs,
                    "updates": updates,
                    "metrics": metrics,
                    "nonzero_weights": len(clean_weights(weights)),
                }
            )
        if updates == 0:
            break

    return weights, {"trainable_records": len(usable), "epochs": history}


def learned_top5(record, weights):
    return reranked_top5(record, weights)


def compare_tops(records, learned_tops, limit):
    changes = []
    counts = Counter()
    for record, learned in zip(records, learned_tops):
        baseline = baseline_top5(record)
        gold = record["gold"]
        baseline_rank = gold_rank(gold, baseline)
        learned_rank = gold_rank(gold, learned)
        if baseline_rank == 0 and learned_rank > 0:
            counts["recovered_top5"] += 1
        elif baseline_rank > 0 and learned_rank == 0:
            counts["lost_top5"] += 1
        elif learned_rank > 0 and (baseline_rank == 0 or learned_rank < baseline_rank):
            counts["improved_rank"] += 1
        elif baseline_rank > 0 and learned_rank > baseline_rank:
            counts["worsened_rank"] += 1

        if baseline == learned or baseline_rank == learned_rank:
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
                "baseline_rank": baseline_rank,
                "learned_rank": learned_rank,
                "baseline_top5": baseline,
                "learned_top5": learned,
            }
        )
    changes.sort(
        key=lambda item: (
            item["learned_rank"] == 0,
            item["learned_rank"] if item["learned_rank"] > 0 else 99,
            99 if item["baseline_rank"] == 0 else item["baseline_rank"],
        )
    )
    return dict(sorted(counts.items())), changes[:limit]


def top_weights(weights, limit):
    cleaned = clean_weights(weights)
    positive = sorted(cleaned.items(), key=lambda item: (-item[1], item[0]))[:limit]
    negative = sorted(cleaned.items(), key=lambda item: (item[1], item[0]))[:limit]
    return {
        "positive": dict(positive),
        "negative": dict(negative),
    }


def decision(primary_metric, guardrails, delta):
    required = (primary_metric, *guardrails)
    guardrails_pass = all(delta.get(metric, 0.0) >= 0 for metric in guardrails)
    primary_improves = delta.get(primary_metric, 0.0) > 0
    neutral = guardrails_pass and delta.get(primary_metric, 0.0) == 0
    status = "pass" if primary_improves and guardrails_pass else "neutral" if neutral else "hold"
    return {
        "status": status,
        "primary_metric": primary_metric,
        "guardrails": list(guardrails),
        "delta_vs_baseline": delta,
    }


def cross_validated_report(records, feature_names, args, guardrails):
    baseline_tops = []
    learned_tops = []
    heldout_records = []
    fold_reports = []
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

        weights, training = train_ranker(train, feature_names, args)
        fold_baseline_tops = [(record["gold"], baseline_top5(record)) for record in holdout]
        fold_learned_top_values = [learned_top5(record, weights) for record in holdout]
        fold_learned_tops = [(record["gold"], top) for record, top in zip(holdout, fold_learned_top_values)]
        baseline_tops.extend(fold_baseline_tops)
        learned_tops.extend(fold_learned_tops)
        heldout_records.extend(holdout)

        baseline = evaluate_gold_tops(fold_baseline_tops)
        learned = evaluate_gold_tops(fold_learned_tops)
        fold_reports.append(
            {
                "fold": fold,
                "train_records": len(train),
                "holdout_records": len(holdout),
                "trainable_records": training["trainable_records"],
                "baseline": baseline,
                "learned": learned,
                "delta_vs_baseline": metrics_delta(learned, baseline),
                "top_weights": top_weights(weights, args.top_weights),
                "training_history": training["epochs"],
            }
        )

    baseline = evaluate_gold_tops(baseline_tops)
    learned = evaluate_gold_tops(learned_tops)
    delta = metrics_delta(learned, baseline)
    outcome_counts, changed_cases = compare_tops(heldout_records, [top for _, top in learned_tops], args.top_moves)
    return {
        "folds": args.cv_folds,
        "evaluated_records": len(heldout_records),
        "baseline": baseline,
        "learned": learned,
        "delta_vs_baseline": delta,
        "decision": decision(args.metric, guardrails, delta),
        "outcome_counts": outcome_counts,
        "changed_cases": changed_cases,
        "fold_reports": fold_reports,
    }


def build_report(args, payload):
    feature_names = tuple(feature.strip() for feature in args.features.split(",") if feature.strip()) if args.features else DEFAULT_FEATURES
    raw_records = list(iter_benchmark_records(payload))
    records, skipped = prepare_records(raw_records, feature_names)
    if not records:
        raise SystemExit("no benchmark records with snapzip_diagnostics were found")
    if args.cv_folds < 2:
        raise SystemExit("--cv-folds must be at least 2")

    guardrails = parse_metric_list(args.guardrails)
    cv_report = cross_validated_report(records, feature_names, args, guardrails)
    final_weights, final_training = train_ranker(records, feature_names, args)
    final_baseline = evaluate_records(records)
    final_learned = evaluate_records(records, final_weights)
    gold_in_candidates = sum(1 for record in records if find_gold_candidate(record) is not None)
    return {
        "input": str(args.input),
        "model": "pairwise_linear_ranker",
        "params": {
            "metric": args.metric,
            "guardrails": list(guardrails),
            "cv_folds": args.cv_folds,
            "epochs": args.epochs,
            "learning_rate": args.learning_rate,
            "l2": args.l2,
            "margin": args.margin,
            "initial_profile": args.initial_profile,
            "freeze_anchor": args.freeze_anchor,
            "negative_strategy": args.negative_strategy,
            "max_negatives": args.max_negatives,
            "seed": args.seed,
        },
        "feature_count": len(feature_names),
        "features": list(feature_names),
        "record_counts": {
            "raw_records": len(raw_records),
            "diagnostic_records": len(records),
            "gold_in_returned_candidates": gold_in_candidates,
            **skipped,
        },
        "limitation": "This learns only within SnapZip's returned diagnostic candidates; it cannot recover gold snippets absent from that candidate set.",
        "cross_validated": cv_report,
        "final_model": {
            "weights": clean_weights(final_weights),
            "top_weights": top_weights(final_weights, args.top_weights),
            "training": final_training,
            "baseline_all": final_baseline,
            "learned_all": final_learned,
            "delta_vs_baseline_all": metrics_delta(final_learned, final_baseline),
        },
    }


def print_metrics(prefix, metrics):
    print(
        f"{prefix}: "
        f"hit@1={metrics['hit@1']:.6f} hit@3={metrics['hit@3']:.6f} "
        f"hit@5={metrics['hit@5']:.6f} mrr@5={metrics['mrr@5']:.6f} ndcg@5={metrics['ndcg@5']:.6f}"
    )


def print_delta(prefix, delta):
    print(
        f"{prefix}: "
        f"hit@1={delta['hit@1']:+.6f} hit@3={delta['hit@3']:+.6f} "
        f"hit@5={delta['hit@5']:+.6f} mrr@5={delta['mrr@5']:+.6f} ndcg@5={delta['ndcg@5']:+.6f}"
    )


def print_report(report):
    counts = report["record_counts"]
    print(f"Loaded {counts['diagnostic_records']} diagnostic records from {report['input']}")
    print(f"Gold present in returned candidates: {counts['gold_in_returned_candidates']}/{counts['diagnostic_records']}")
    print("Limitation: learned reranking cannot recover candidates SnapZip did not return.")
    cv_report = report["cross_validated"]
    print_metrics(f"Cross-validated baseline ({cv_report['folds']} folds)", cv_report["baseline"])
    print_metrics("Cross-validated learned", cv_report["learned"])
    print_delta("Cross-validated delta", cv_report["delta_vs_baseline"])
    decision_report = cv_report["decision"]
    guardrail_text = ",".join(decision_report["guardrails"]) if decision_report["guardrails"] else "none"
    print(
        f"Decision: {decision_report['status']} on {decision_report['primary_metric']} "
        f"with guardrails {guardrail_text}"
    )
    if cv_report["outcome_counts"]:
        print(f"Outcome counts: {cv_report['outcome_counts']}")
    weights = report["final_model"]["weights"]
    if weights:
        print("Final model nonzero weights:")
        for feature, weight in weights.items():
            print(f"  {feature}: {weight}")
    else:
        print("Final model nonzero weights: none")
    final_delta = report["final_model"]["delta_vs_baseline_all"]
    print_delta("Final model in-sample delta", final_delta)


def parse_args():
    parser = argparse.ArgumentParser(description="Cross-validate an offline pairwise SnapZip diagnostics ranker")
    parser.add_argument("--input", required=True, type=Path, help="Benchmark JSON written by benchmarks/run.py")
    parser.add_argument("--json", type=Path, default=None, help="Optional path for the learner report JSON")
    parser.add_argument("--metric", choices=("hit@1", "hit@3", "hit@5", "mrr@5", "ndcg@5"), default="mrr@5")
    parser.add_argument("--guardrails", default="hit@5", help="Comma-separated validation metrics that must not regress, or none")
    parser.add_argument("--features", default="", help="Comma-separated feature allowlist")
    parser.add_argument("--cv-folds", type=int, default=5, help="Deterministic cross-validation folds")
    parser.add_argument("--epochs", type=int, default=25, help="Pairwise training epochs")
    parser.add_argument("--learning-rate", type=float, default=0.05, help="Pairwise update learning rate")
    parser.add_argument("--l2", type=float, default=0.001, help="L2 shrinkage applied on violating pairs")
    parser.add_argument("--margin", type=float, default=0.05, help="Required gold-vs-negative score margin")
    parser.add_argument("--max-abs-weight", type=float, default=5.0, help="Clamp absolute feature weights; use 0 to disable")
    parser.add_argument("--initial-profile", choices=("baseline", "runtime", "zero"), default="baseline")
    parser.add_argument("--freeze-anchor", action="store_true", help="Keep runtime_score/runtime_rank_recip fixed while learning residual weights")
    parser.add_argument("--negative-strategy", choices=("hard", "all"), default="hard")
    parser.add_argument("--max-negatives", type=int, default=3, help="Maximum negatives per record per epoch; use 0 for all")
    parser.add_argument("--history-every", type=int, default=5, help="Training history interval in epochs")
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--top-weights", type=int, default=8)
    parser.add_argument("--top-moves", type=int, default=10)
    return parser.parse_args()


def main():
    args = parse_args()
    if args.epochs <= 0:
        raise SystemExit("--epochs must be positive")
    if args.history_every <= 0:
        raise SystemExit("--history-every must be positive")
    if args.max_negatives < 0:
        raise SystemExit("--max-negatives must be zero or greater")
    with args.input.expanduser().open("r", encoding="utf-8") as handle:
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
