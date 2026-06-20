import importlib.util
import json
import random
import sys
import time
from pathlib import Path


def load_module(path):
    spec = importlib.util.spec_from_file_location("candidate_rbt", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def node_value(node):
    return getattr(node, "val", getattr(node, "value", None))


def validate_tree(tree, expected_values):
    nil = tree.nil
    root = tree.root
    black = False
    red = True

    if getattr(nil, "color", black) != black:
        raise AssertionError("sentinel NIL node is not black")

    if root == nil:
        if expected_values:
            raise AssertionError("tree is empty but expected values remain")
        return

    if root.color != black:
        raise AssertionError("root is not black")

    inorder = []

    def walk(node, lower, upper, parent):
        if node == nil:
            return 1

        value = node_value(node)
        if value is None:
            raise AssertionError("node has no val/value field")
        if not (lower < value < upper):
            raise AssertionError(f"BST order violated at {value}")
        if node.parent != parent:
            raise AssertionError(f"parent pointer mismatch at {value}")

        if node.color == red:
            if node.left.color != black or node.right.color != black:
                raise AssertionError(f"red node {value} has a red child")

        left_black_height = walk(node.left, lower, value, node)
        inorder.append(value)
        right_black_height = walk(node.right, value, upper, node)

        if left_black_height != right_black_height:
            raise AssertionError(
                f"black-height mismatch at {value}: left={left_black_height}, right={right_black_height}"
            )

        if node.color == black:
            return left_black_height + 1
        return left_black_height

    walk(root, float("-inf"), float("inf"), nil)

    expected_sorted = sorted(expected_values)
    if inorder != expected_sorted:
        raise AssertionError(f"in-order values mismatch: got={inorder}, want={expected_sorted}")


def run_stress(candidate_path):
    module = load_module(candidate_path)
    tree = module.RedBlackTree()

    deterministic_values = [41, 38, 31, 12, 19, 8, 55, 60, 1, 7, 13, 17, 25, 44, 50]
    current = set()

    for value in deterministic_values:
        tree.insert(value)
        current.add(value)
        validate_tree(tree, current)

    for value in [8, 12, 41, 55, 1]:
        tree.delete(value)
        current.remove(value)
        validate_tree(tree, current)

    rng = random.Random(20260620)
    remaining = list(range(1, 121))
    rng.shuffle(remaining)

    tree = module.RedBlackTree()
    current = set()
    for value in remaining:
        tree.insert(value)
        current.add(value)
        validate_tree(tree, current)

    deletions = remaining[:80]
    for value in deletions:
        tree.delete(value)
        current.remove(value)
        validate_tree(tree, current)

    return {
        "deterministic_inserts": len(deterministic_values),
        "deterministic_deletes": 5,
        "random_inserts": 120,
        "random_deletes": 80,
        "final_size": len(current),
    }


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: python3 stress_rbt.py <candidate.py>")

    candidate_path = Path(sys.argv[1]).resolve()
    started = time.perf_counter()
    try:
        details = run_stress(candidate_path)
    except Exception as exc:
        elapsed = time.perf_counter() - started
        print(json.dumps({
            "candidate": str(candidate_path),
            "passed": False,
            "elapsed_seconds": round(elapsed, 6),
            "error": str(exc),
        }, indent=2))
        return 1

    elapsed = time.perf_counter() - started
    print(json.dumps({
        "candidate": str(candidate_path),
        "passed": True,
        "elapsed_seconds": round(elapsed, 6),
        "details": details,
    }, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
