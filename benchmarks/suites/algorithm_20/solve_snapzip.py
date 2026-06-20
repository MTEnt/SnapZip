import argparse
import os
import subprocess
import time


TASK_TOPICS = {
    "task_1_anagram.py": "Anagram Detection",
    "task_2_lru_cache.py": "LRU Cache",
    "task_3_matrix_transpose.py": "Matrix Transpose",
    "task_4_binary_search.py": "Binary Search",
    "task_5_merge_intervals.py": "Merge Intervals",
    "task_6_trie_wildcard.py": "Trie with Wildcard Search",
    "task_7_red_black_tree.py": "Red-Black Tree",
    "task_8_quicksort.py": "Quicksort Algorithm",
    "task_9_fibonacci.py": "Dynamic Programming Fibonacci",
    "task_10_json_parser.py": "Simple JSON Parser",
    "task_11_string_compress.py": "Run-length String Compression",
    "task_12_dijkstra.py": "Dijkstra Shortest Path",
    "task_13_lcs.py": "Longest Common Subsequence",
    "task_14_tree_dfs.py": "Binary Tree DFS Traversal",
    "task_15_knapsack.py": "0/1 Knapsack Problem",
    "task_16_levenshtein.py": "Levenshtein Edit Distance",
    "task_17_heapsort.py": "Heapsort Algorithm",
    "task_18_regex_match.py": "Basic Regex Match",
    "task_19_topological_sort.py": "Topological Sort",
    "task_20_astar.py": "Astar Search",
}


def parse_task_selection(value):
    if not value:
        return list(TASK_TOPICS.keys())

    selected = []
    for raw_name in value.split(","):
        name = raw_name.strip()
        if not name:
            continue
        if name.isdigit():
            name = f"task_{int(name)}_"
            matches = [task for task in TASK_TOPICS if task.startswith(name)]
            if len(matches) != 1:
                raise ValueError(f"could not resolve task number {raw_name!r}")
            name = matches[0]
        if name not in TASK_TOPICS:
            raise ValueError(f"unknown task {raw_name!r}")
        selected.append(name)
    return selected


def extract_templates(selected_tasks):
    with open("reference_templates/code_templates.py", "r", encoding="utf-8") as f:
        content = f.read()

    topics = content.split("# Topic: ")
    templates = {}
    imports_header = (
        "from typing import List, Dict, Tuple, Any\n"
        "from collections import OrderedDict\n"
        "import heapq\n"
        "import json\n\n"
    )

    for task_name in selected_tasks:
        header_keyword = TASK_TOPICS[task_name]
        for part in topics:
            if part.startswith(header_keyword):
                templates[task_name] = imports_header + "# Topic: " + part.strip() + "\n"
                break
        else:
            print(f"Warning: template for {task_name} (keyword: {header_keyword!r}) not found")

    return templates


def run_snapzip_solver(args):
    selected_tasks = parse_task_selection(args.tasks)
    templates = extract_templates(selected_tasks)
    os.makedirs("tasks", exist_ok=True)

    start_time = time.time()

    for task_name, code in templates.items():
        sketch_path = f"sketch_{task_name}"
        with open(sketch_path, "w", encoding="utf-8") as f:
            f.write(code)

        dest_path = os.path.join("tasks", task_name)
        cmd = [
            args.snapzip_bin,
            "optimize",
            "--sketch",
            sketch_path,
            "--context",
            args.context,
            "--output",
            dest_path,
            "--iter",
            str(args.iterations),
            "--temp",
            str(args.temperature),
            "--prior-weight",
            str(args.prior_weight),
        ]

        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            print(f"Error optimizing {task_name}: {result.stderr}")
            with open(dest_path, "w", encoding="utf-8") as f:
                f.write(code)
        else:
            print(f"Optimized and wrote: {task_name}")

        if os.path.exists(sketch_path):
            os.remove(sketch_path)

    elapsed = time.time() - start_time
    print(f"\nSnapZip optimization finished in {elapsed:.4f} seconds!")


def main():
    parser = argparse.ArgumentParser(description="Solve the 20-task benchmark with SnapZip.")
    parser.add_argument("--snapzip-bin", default=os.environ.get("SNAPZIP_BIN", "snapzip"))
    parser.add_argument("--context", default="reference_templates")
    parser.add_argument("--iterations", type=int, default=100)
    parser.add_argument("--temperature", type=float, default=0.15)
    parser.add_argument("--prior-weight", type=float, default=1.0)
    parser.add_argument("--tasks", default="", help="Comma-separated task numbers or filenames")
    args = parser.parse_args()
    run_snapzip_solver(args)


if __name__ == "__main__":
    main()
