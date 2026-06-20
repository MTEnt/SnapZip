import argparse
import os

def write_raw_solutions(task_selection=""):
    solutions = {
        "task_1_anagram.py": """# Task 1: Anagram Detection
def is_anagram(s1: str, s2: str) -> bool:
    return sorted(s1.replace(" ", "").lower()) == sorted(s2.replace(" ", "").lower())
""",
        "task_2_lru_cache.py": """# Task 2: LRU Cache
class LRUCache:
    def __init__(self, capacity: int):
        self.capacity = capacity
        self.cache = {}
        self.order = []

    def get(self, key: int) -> int:
        if key not in self.cache:
            return -1
        self.order.remove(key)
        self.order.append(key)
        return self.cache[key]

    def put(self, key: int, value: int) -> None:
        if key in self.cache:
            self.order.remove(key)
        elif len(self.cache) >= self.capacity:
            oldest = self.order.pop(0)
            del self.cache[oldest]
        self.cache[key] = value
        self.order.append(key)
""",
        "task_3_matrix_transpose.py": """# Task 3: Matrix Transpose
from typing import List

def transpose(matrix: List[List[int]]) -> List[List[int]]:
    if not matrix or not matrix[0]:
        return []
    return [[matrix[r][c] for r in range(len(matrix))] for c in range(len(matrix[0]))]
""",
        "task_4_binary_search.py": """# Task 4: Binary Search
from typing import List

def binary_search(nums: List[int], target: int) -> int:
    l, r = 0, len(nums) - 1
    while l <= r:
        mid = (l + r) // 2
        if nums[mid] == target:
            return mid
        elif nums[mid] < target:
            l = mid + 1
        else:
            r = mid - 1
    return -1
""",
        "task_5_merge_intervals.py": """# Task 5: Merge Intervals
from typing import List

def merge_intervals(intervals: List[List[int]]) -> List[List[int]]:
    if not intervals:
        return []
    intervals.sort(key=lambda x: x[0])
    res = [intervals[0]]
    for start, end in intervals[1:]:
        if start <= res[-1][1]:
            res[-1][1] = max(res[-1][1], end)
        else:
            res.append([start, end])
    return res
""",
        "task_6_trie_wildcard.py": """# Task 6: Trie with Wildcard Search and Custom Serialization
import json

class TrieNode:
    def __init__(self):
        self.children = {}
        self.is_word = False

class Trie:
    def __init__(self):
        self.root = TrieNode()

    def insert(self, word: str) -> None:
        node = self.root
        for char in word:
            if char not in node.children:
                node.children[char] = TrieNode()
            node = node.children[char]
        node.is_word = True

    def search(self, word: str) -> bool:
        def dfs(node, idx):
            if idx == len(word):
                return node.is_word
            char = word[idx]
            if char == '.':
                for child in node.children.values():
                    if dfs(child, idx + 1):
                        return True
                return False
            else:
                if char not in node.children:
                    return False
                return dfs(node.children[char], idx + 1)
        return dfs(self.root, 0)

    def serialize(self) -> str:
        def serialize_node(node):
            return {
                "is_word": node.is_word,
                "children": {char: serialize_node(child) for char, child in node.children.items()}
            }
        return json.dumps(serialize_node(self.root))

    @classmethod
    def deserialize(cls, data: str) -> 'Trie':
        trie = cls()
        obj = json.loads(data)
        def deserialize_node(node, node_dict):
            node.is_word = node_dict["is_word"]
            for char, child_dict in node_dict["children"].items():
                child = TrieNode()
                node.children[char] = child
                deserialize_node(child, child_dict)
        deserialize_node(trie.root, obj)
        return trie
""",
        "task_7_red_black_tree.py": """# Task 7: Red-Black Tree Implementation (Standard BST representation - fails invariants)
RED = True
BLACK = False

class Node:
    def __init__(self, val: int, color: bool = RED):
        self.val = val
        self.color = color
        self.left = None
        self.right = None
        self.parent = None

class RedBlackTree:
    def __init__(self):
        self.nil = Node(0, BLACK)
        self.root = self.nil

    def insert(self, val: int) -> None:
        z = Node(val, RED)
        z.left = self.nil
        z.right = self.nil
        z.parent = self.nil

        y = self.nil
        x = self.root
        while x != self.nil:
            y = x
            if z.val < x.val:
                x = x.left
            else:
                x = x.right
        z.parent = y
        if y == self.nil:
            self.root = z
        elif z.val < y.val:
            y.left = z
        else:
            y.right = z

    def delete(self, val: int) -> None:
        z = self.root
        while z != self.nil:
            if z.val == val:
                break
            elif val < z.val:
                z = z.left
            else:
                z = z.right
        if z == self.nil:
            return

        if z.left == self.nil:
            self.transplant(z, z.right)
        elif z.right == self.nil:
            self.transplant(z, z.left)
        else:
            y = self.minimum(z.right)
            if y.parent != z:
                self.transplant(y, y.right)
                y.right = z.right
                y.right.parent = y
            self.transplant(z, y)
            y.left = z.left
            y.left.parent = y

    def transplant(self, u, v):
        if u.parent == self.nil:
            self.root = v
        elif u == u.parent.left:
            u.parent.left = v
        else:
            u.parent.right = v
        v.parent = u.parent

    def minimum(self, x):
        while x.left != self.nil:
            x = x.left
        return x
""",
        "task_8_quicksort.py": """# Task 8: Quicksort Algorithm
from typing import List

def quicksort(arr: List[int]) -> List[int]:
    if len(arr) <= 1:
        return arr
    pivot = arr[0]
    left = [x for x in arr[1:] if x < pivot]
    right = [x for x in arr[1:] if x >= pivot]
    return quicksort(left) + [pivot] + quicksort(right)
""",
        "task_9_fibonacci.py": """# Task 9: Dynamic Programming Fibonacci
def fib(n: int) -> int:
    if n <= 1:
        return n
    dp = [0] * (n + 1)
    dp[1] = 1
    for i in range(2, n + 1):
        dp[i] = dp[i-1] + dp[i-2]
    return dp[n]
""",
        "task_10_json_parser.py": """# Task 10: Simple JSON Parser subset
from typing import Any
import json

def parse_json(s: str) -> Any:
    return json.loads(s)
""",
        "task_11_string_compress.py": """# Task 11: Run-length String Compression
def compress_string(s: str) -> str:
    if not s:
        return ""
    res = []
    curr = s[0]
    count = 1
    for c in s[1:]:
        if c == curr:
            count += 1
        else:
            res.append(curr + str(count))
            curr = c
            count = 1
    res.append(curr + str(count))
    compressed = "".join(res)
    return compressed if len(compressed) < len(s) else s
""",
        "task_12_dijkstra.py": """# Task 12: Dijkstra's Graph Shortest Path
from typing import List, Dict, Tuple
import heapq

def dijkstra(graph: Dict[int, List[Tuple[int, int]]], start: int, end: int) -> int:
    heap = [(0, start)]
    dist_map = {start: 0}
    while heap:
        d, u = heapq.heappop(heap)
        if u == end:
            return d
        if d > dist_map.get(u, float('inf')):
            continue
        for v, w in graph.get(u, []):
            if d + w < dist_map.get(v, float('inf')):
                dist_map[v] = d + w
                heapq.heappush(heap, (d + w, v))
    return -1
""",
        "task_13_lcs.py": """# Task 13: Longest Common Subsequence (LCS)
def lcs(s1: str, s2: str) -> int:
    m, n = len(s1), len(s2)
    dp = [[0] * (n + 1) for _ in range(m + 1)]
    for i in range(1, m + 1):
        for j in range(1, n + 1):
            if s1[i-1] == s2[j-1]:
                dp[i][j] = dp[i-1][j-1] + 1
            else:
                dp[i][j] = max(dp[i-1][j], dp[i][j-1])
    return dp[m][n]
""",
        "task_14_tree_dfs.py": """# Task 14: Binary Tree DFS Traversal
from typing import List

class TreeNode:
    def __init__(self, val: int):
        self.val = val
        self.left = None
        self.right = None

def dfs_inorder(root: TreeNode) -> List[int]:
    res = []
    def helper(node):
        if not node:
            return
        helper(node.left)
        res.append(node.val)
        helper(node.right)
    helper(root)
    return res
""",
        "task_15_knapsack.py": """# Task 15: 0/1 Knapsack Problem
from typing import List

def knapsack(weights: List[int], values: List[int], capacity: int) -> int:
    dp = [0] * (capacity + 1)
    for w, v in zip(weights, values):
        for c in range(capacity, w - 1, -1):
            dp[c] = max(dp[c], dp[c - w] + v)
    return dp[capacity]
""",
        "task_16_levenshtein.py": """# Task 16: Levenshtein Edit Distance
def edit_distance(s1: str, s2: str) -> int:
    m, n = len(s1), len(s2)
    dp = [[0] * (n + 1) for _ in range(m + 1)]
    for i in range(m + 1):
        dp[i][0] = i
    for j in range(n + 1):
        dp[0][j] = j
    for i in range(1, m + 1):
        for j in range(1, n + 1):
            if s1[i-1] == s2[j-1]:
                dp[i][j] = dp[i-1][j-1]
            else:
                dp[i][j] = min(dp[i-1][j] + 1, dp[i][j-1] + 1, dp[i-1][j-1] + 1)
    return dp[m][n]
""",
        "task_17_heapsort.py": """# Task 17: Heapsort Algorithm
from typing import List
import heapq

def heapsort(arr: List[int]) -> List[int]:
    h = []
    for x in arr:
        heapq.heappush(h, x)
    return [heapq.heappop(h) for _ in range(len(arr))]
""",
        "task_18_regex_match.py": """# Task 18: Basic Regex Match (. and * only)
def is_match(text: str, pattern: str) -> bool:
    dp = [[False] * (len(pattern) + 1) for _ in range(len(text) + 1)]
    dp[0][0] = True
    for j in range(1, len(pattern) + 1):
        if pattern[j-1] == '*':
            dp[0][j] = dp[0][j-2]
    for i in range(1, len(text) + 1):
        for j in range(1, len(pattern) + 1):
            if pattern[j-1] == '.' or pattern[j-1] == text[i-1]:
                dp[i][j] = dp[i-1][j-1]
            elif pattern[j-1] == '*':
                dp[i][j] = dp[i][j-2]
                if pattern[j-2] == '.' or pattern[j-2] == text[i-1]:
                    dp[i][j] = dp[i][j] or dp[i-1][j]
    return dp[len(text)][len(pattern)]
""",
        "task_19_topological_sort.py": """# Task 19: Topological Sort (DAG)
from typing import List, Dict

def topological_sort(num_nodes: int, edges: List[List[int]]) -> List[int]:
    adj = {i: [] for i in range(num_nodes)}
    in_degree = [0] * num_nodes
    for u, v in edges:
        adj[u].append(v)
        in_degree[v] += 1
    queue = [i for i in range(num_nodes) if in_degree[i] == 0]
    order = []
    while queue:
        u = queue.pop(0)
        order.append(u)
        for v in adj[u]:
            in_degree[v] -= 1
            if in_degree[v] == 0:
                queue.append(v)
    return order if len(order) == num_nodes else []
""",
        "task_20_astar.py": """# Task 20: A* Search Pathfinder on a 2D Grid
from typing import List, Tuple
import heapq

def astar(grid: List[List[int]], start: Tuple[int, int], end: Tuple[int, int]) -> int:
    R, C = len(grid), len(grid[0])
    heap = [(abs(start[0]-end[0])+abs(start[1]-end[1]), 0, start[0], start[1])]
    visited = {}
    while heap:
        f, d, r, c = heapq.heappop(heap)
        if (r, c) == end:
            return d
        if (r, c) in visited and visited[(r, c)] <= d:
            continue
        visited[(r, c)] = d
        for dr, dc in [(-1, 0), (1, 0), (0, -1), (0, 1)]:
            nr, nc = r + dr, c + dc
            if 0 <= nr < R and 0 <= nc < C and grid[nr][nc] == 0:
                h = abs(nr-end[0]) + abs(nc-end[1])
                heapq.heappush(heap, (d + 1 + h, d + 1, nr, nc))
    return -1
"""
    }

    selected_tasks = parse_task_selection(task_selection, solutions)

    for name in selected_tasks:
        content = solutions[name]
        with open(os.path.join("tasks", name), "w") as f:
            f.write(content)
    print(f"Wrote {len(selected_tasks)} raw baseline solution(s)!")

def parse_task_selection(value, solutions):
    if not value:
        return list(solutions.keys())

    selected = []
    for raw_name in value.split(","):
        name = raw_name.strip()
        if not name:
            continue
        if name.isdigit():
            prefix = f"task_{int(name)}_"
            matches = [task for task in solutions if task.startswith(prefix)]
            if len(matches) != 1:
                raise ValueError(f"could not resolve task number {raw_name!r}")
            name = matches[0]
        if name not in solutions:
            raise ValueError(f"unknown task {raw_name!r}")
        selected.append(name)
    return selected

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Write raw baseline benchmark solutions.")
    parser.add_argument("--tasks", default="", help="Comma-separated task numbers or filenames")
    args = parser.parse_args()
    write_raw_solutions(args.tasks)
