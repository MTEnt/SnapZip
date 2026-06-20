import os

def setup():
    # 1. Create Directories if they don't exist
    os.makedirs("tasks", exist_ok=True)
    os.makedirs("reference_templates", exist_ok=True)

    # 2. Define Task Stubs
    stubs = {
        "task_1_anagram.py": """# Task 1: Anagram Detection
def is_anagram(s1: str, s2: str) -> bool:
    # TODO: Implement anagram check
    pass
""",
        "task_2_lru_cache.py": """# Task 2: LRU Cache
class LRUCache:
    def __init__(self, capacity: int):
        # TODO: Initialize LRU Cache
        pass

    def get(self, key: int) -> int:
        # TODO: Get key value
        return -1

    def put(self, key: int, value: int) -> None:
        # TODO: Put key and value
        pass
""",
        "task_3_matrix_transpose.py": """# Task 3: Matrix Transpose
from typing import List

def transpose(matrix: List[List[int]]) -> List[List[int]]:
    # TODO: Implement matrix transpose
    pass
""",
        "task_4_binary_search.py": """# Task 4: Binary Search
from typing import List

def binary_search(nums: List[int], target: int) -> int:
    # TODO: Implement binary search returning index or -1
    pass
""",
        "task_5_merge_intervals.py": """# Task 5: Merge Intervals
from typing import List

def merge_intervals(intervals: List[List[int]]) -> List[List[int]]:
    # TODO: Implement merge intervals
    pass
""",
        "task_6_trie_wildcard.py": """# Task 6: Trie with Wildcard Search and Custom Serialization
class TrieNode:
    def __init__(self):
        # TODO: Initialize node
        pass

class Trie:
    def __init__(self):
        # TODO: Initialize trie
        pass

    def insert(self, word: str) -> None:
        # TODO: Insert word
        pass

    def search(self, word: str) -> bool:
        # TODO: Search word (supports '.' as a wildcard matching any character)
        pass

    def serialize(self) -> str:
        # TODO: Return serialized pre-order string representation of the trie
        pass

    @classmethod
    def deserialize(cls, data: str) -> 'Trie':
        # TODO: Reconstruct the Trie from serialized data string
        pass
""",
        "task_7_red_black_tree.py": """# Task 7: Red-Black Tree Implementation
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
        self.nil = Node(0, BLACK)  # Sentinel NIL leaf node
        self.root = self.nil

    def insert(self, val: int) -> None:
        # TODO: Implement self-balancing insertion
        pass

    def delete(self, val: int) -> None:
        # TODO: Implement self-balancing deletion
        pass
""",
        "task_8_quicksort.py": """# Task 8: Quicksort Algorithm
from typing import List

def quicksort(arr: List[int]) -> List[int]:
    # TODO: Implement quicksort
    pass
""",
        "task_9_fibonacci.py": """# Task 9: Dynamic Programming Fibonacci
def fib(n: int) -> int:
    # TODO: Implement fibonacci
    pass
""",
        "task_10_json_parser.py": """# Task 10: Simple JSON Parser subset
from typing import Any

def parse_json(s: str) -> Any:
    # TODO: Parse basic JSON string (dicts and arrays only)
    pass
""",
        "task_11_string_compress.py": """# Task 11: Run-length String Compression
def compress_string(s: str) -> str:
    # TODO: Implement string compression
    pass
""",
        "task_12_dijkstra.py": """# Task 12: Dijkstra's Graph Shortest Path
from typing import List, Dict, Tuple

def dijkstra(graph: Dict[int, List[Tuple[int, int]]], start: int, end: int) -> int:
    # TODO: Find shortest path distance from start to end (or -1 if unreachable)
    pass
""",
        "task_13_lcs.py": """# Task 13: Longest Common Subsequence (LCS)
def lcs(s1: str, s2: str) -> int:
    # TODO: Implement longest common subsequence length
    pass
""",
        "task_14_tree_dfs.py": """# Task 14: Binary Tree DFS Traversal
from typing import List

class TreeNode:
    def __init__(self, val: int):
        self.val = val
        self.left = None
        self.right = None

def dfs_inorder(root: TreeNode) -> List[int]:
    # TODO: Implement inorder DFS traversal
    pass
""",
        "task_15_knapsack.py": """# Task 15: 0/1 Knapsack Problem
from typing import List

def knapsack(weights: List[int], values: List[int], capacity: int) -> int:
    # TODO: Implement knapsack 0/1 optimal value
    pass
""",
        "task_16_levenshtein.py": """# Task 16: Levenshtein Edit Distance
def edit_distance(s1: str, s2: str) -> int:
    # TODO: Implement Levenshtein edit distance
    pass
""",
        "task_17_heapsort.py": """# Task 17: Heapsort Algorithm
from typing import List

def heapsort(arr: List[int]) -> List[int]:
    # TODO: Implement heapsort
    pass
""",
        "task_18_regex_match.py": """# Task 18: Basic Regex Match (. and * only)
def is_match(text: str, pattern: str) -> bool:
    # TODO: Implement regex matching
    pass
""",
        "task_19_topological_sort.py": """# Task 19: Topological Sort (DAG)
from typing import List, Dict

def topological_sort(num_nodes: int, edges: List[List[int]]) -> List[int]:
    # TODO: Return a valid topological ordering list, or empty list if cycle exists
    pass
""",
        "task_20_astar.py": """# Task 20: A* Search Pathfinder on a 2D Grid
from typing import List, Tuple

def astar(grid: List[List[int]], start: Tuple[int, int], end: Tuple[int, int]) -> int:
    # TODO: Return shortest path length from start to end (or -1 if blocked). Grid: 0=open, 1=blocked.
    pass
"""
    }

    for name, content in stubs.items():
        with open(os.path.join("tasks", name), "w") as f:
            f.write(content)

    # 3. Create code_templates.py reference file
    templates_content = """from typing import List, Dict, Tuple, Any
from collections import OrderedDict
import heapq
import json

# Topic: Anagram Detection check function
def is_anagram(s1: str, s2: str) -> bool:
    clean_s1 = s1.replace(" ", "").lower()
    clean_s2 = s2.replace(" ", "").lower()
    return sorted(clean_s1) == sorted(clean_s2)

# Topic: LRU Cache implementation using OrderedDict
class LRUCache:
    def __init__(self, capacity: int):
        self.capacity = capacity
        self.cache = OrderedDict()

    def get(self, key: int) -> int:
        if key not in self.cache:
            return -1
        self.cache.move_to_end(key)
        return self.cache[key]

    def put(self, key: int, value: int) -> None:
        if key in self.cache:
            self.cache.move_to_end(key)
        self.cache[key] = value
        if len(self.cache) > self.capacity:
            self.cache.popitem(last=False)

# Topic: Matrix Transpose 2D array function
def transpose(matrix: List[List[int]]) -> List[List[int]]:
    if not matrix or not matrix[0]:
        return []
    return [list(row) for row in zip(*matrix)]

# Topic: Binary Search iterative algorithm
def binary_search(nums: List[int], target: int) -> int:
    left, right = 0, len(nums) - 1
    while left <= right:
        mid = left + (right - left) // 2
        if nums[mid] == target:
            return mid
        elif nums[mid] < target:
            left = mid + 1
        else:
            right = mid - 1
    return -1

# Topic: Merge Intervals algorithm
def merge_intervals(intervals: List[List[int]]) -> List[List[int]]:
    if not intervals:
        return []
    intervals.sort(key=lambda x: x[0])
    merged = [intervals[0]]
    for current in intervals[1:]:
        prev_start, prev_end = merged[-1]
        curr_start, curr_end = current
        if curr_start <= prev_end:
            merged[-1][1] = max(prev_end, curr_end)
        else:
            merged.append(current)
    return merged

# Topic: Trie with Wildcard Search and Custom Serialization
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
        def dfs(node, index) -> bool:
            if index == len(word):
                return node.is_word
            char = word[index]
            if char == '.':
                for child in node.children.values():
                    if dfs(child, index + 1):
                        return True
                return False
            else:
                if char not in node.children:
                    return False
                return dfs(node.children[char], index + 1)
        return dfs(self.root, 0)

    def serialize(self) -> str:
        def helper(node) -> str:
            parts = []
            for char, child in node.children.items():
                is_w = "1" if child.is_word else "0"
                parts.append(f"{char}:{is_w}[{helper(child)}]")
            return ",".join(parts)
        return helper(self.root)

    @classmethod
    def deserialize(cls, data: str) -> 'Trie':
        trie = cls()
        if not data:
            return trie
        
        def parse(node, s: str):
            if not s:
                return
            i = 0
            while i < len(s):
                char = s[i]
                is_w = s[i+2] == '1'
                
                start = i + 4
                brackets = 1
                j = start
                while j < len(s) and brackets > 0:
                    if s[j] == '[':
                        brackets += 1
                    elif s[j] == ']':
                        brackets -= 1
                    j += 1
                child_data = s[start:j-1]
                
                child_node = TrieNode()
                child_node.is_word = is_w
                node.children[char] = child_node
                
                parse(child_node, child_data)
                
                i = j
                if i < len(s) and s[i] == ',':
                    i += 1
        
        parse(trie.root, data)
        return trie

# Topic: Red-Black Tree self-balancing binary search tree
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

    def left_rotate(self, x: Node) -> None:
        y = x.right
        x.right = y.left
        if y.left != self.nil:
            y.left.parent = x
        y.parent = x.parent
        if x.parent == self.nil:
            self.root = y
        elif x == x.parent.left:
            x.parent.left = y
        else:
            x.parent.right = y
        y.left = x
        x.parent = y

    def right_rotate(self, y: Node) -> None:
        x = y.left
        y.left = x.right
        if x.right != self.nil:
            x.right.parent = y
        x.parent = y.parent
        if y.parent == self.nil:
            self.root = x
        elif y == y.parent.left:
            y.parent.left = x
        else:
            y.parent.right = x
        x.right = y
        y.parent = x

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
        self.insert_fixup(z)

    def insert_fixup(self, z: Node) -> None:
        while z.parent.color == RED:
            if z.parent == z.parent.parent.left:
                y = z.parent.parent.right
                if y.color == RED:
                    z.parent.color = BLACK
                    y.color = BLACK
                    z.parent.parent.color = RED
                    z = z.parent.parent
                else:
                    if z == z.parent.right:
                        z = z.parent
                        self.left_rotate(z)
                    z.parent.color = BLACK
                    z.parent.parent.color = RED
                    self.right_rotate(z.parent.parent)
            else:
                y = z.parent.parent.left
                if y.color == RED:
                    z.parent.color = BLACK
                    y.color = BLACK
                    z.parent.parent.color = RED
                    z = z.parent.parent
                else:
                    if z == z.parent.left:
                        z = z.parent
                        self.right_rotate(z)
                    z.parent.color = BLACK
                    z.parent.parent.color = RED
                    self.left_rotate(z.parent.parent)
        self.root.color = BLACK

    def transplant(self, u: Node, v: Node) -> None:
        if u.parent == self.nil:
            self.root = v
        elif u == u.parent.left:
            u.parent.left = v
        else:
            u.parent.right = v
        v.parent = u.parent

    def minimum(self, x: Node) -> Node:
        while x.left != self.nil:
            x = x.left
        return x

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

        y = z
        y_original_color = y.color
        if z.left == self.nil:
            x = z.right
            self.transplant(z, z.right)
        elif z.right == self.nil:
            x = z.left
            self.transplant(z, z.left)
        else:
            y = self.minimum(z.right)
            y_original_color = y.color
            x = y.right
            if y.parent == z:
                x.parent = y
            else:
                self.transplant(y, y.right)
                y.right = z.right
                y.right.parent = y
            self.transplant(z, y)
            y.left = z.left
            y.left.parent = y
            y.color = z.color
        if y_original_color == BLACK:
            self.delete_fixup(x)

    def delete_fixup(self, x: Node) -> None:
        while x != self.root and x.color == BLACK:
            if x == x.parent.left:
                w = x.parent.right
                if w.color == RED:
                    w.color = BLACK
                    x.parent.color = RED
                    self.left_rotate(x.parent)
                    w = x.parent.right
                if w.left.color == BLACK and w.right.color == BLACK:
                    w.color = RED
                    x = x.parent
                else:
                    if w.right.color == BLACK:
                        w.left.color = BLACK
                        w.color = RED
                        self.right_rotate(w)
                        w = x.parent.right
                    w.color = x.parent.color
                    x.parent.color = BLACK
                    w.right.color = BLACK
                    self.left_rotate(x.parent)
                    x = self.root
            else:
                w = x.parent.left
                if w.color == RED:
                    w.color = BLACK
                    x.parent.color = RED
                    self.right_rotate(x.parent)
                    w = x.parent.left
                if w.right.color == BLACK and w.left.color == BLACK:
                    w.color = RED
                    x = x.parent
                else:
                    if w.left.color == BLACK:
                        w.right.color = BLACK
                        w.color = RED
                        self.left_rotate(w)
                        w = x.parent.left
                    w.color = x.parent.color
                    x.parent.color = BLACK
                    w.left.color = BLACK
                    self.right_rotate(x.parent)
                    x = self.root
        x.color = BLACK

# Topic: Quicksort Algorithm pivot sort
def quicksort(arr: List[int]) -> List[int]:
    if len(arr) <= 1:
        return arr
    pivot = arr[len(arr) // 2]
    left = [x for x in arr if x < pivot]
    middle = [x for x in arr if x == pivot]
    right = [x for x in arr if x > pivot]
    return quicksort(left) + middle + quicksort(right)

# Topic: Dynamic Programming Fibonacci memoization
def fib(n: int) -> int:
    if n <= 1:
        return n
    memo = [0] * (n + 1)
    memo[1] = 1
    for i in range(2, n + 1):
        memo[i] = memo[i-1] + memo[i-2]
    return memo[n]

# Topic: Simple JSON Parser dictionaries and arrays
def parse_json(s: str) -> Any:
    return json.loads(s)

# Topic: Run-length String Compression
def compress_string(s: str) -> str:
    if not s:
        return ""
    parts = []
    current_char = s[0]
    count = 1
    for char in s[1:]:
        if char == current_char:
            count += 1
        else:
            parts.append(f"{current_char}{count}")
            current_char = char
            count = 1
    parts.append(f"{current_char}{count}")
    res = "".join(parts)
    return res if len(res) < len(s) else s

# Topic: Dijkstra Shortest Path graph distance min-heap
def dijkstra(graph: Dict[int, List[Tuple[int, int]]], start: int, end: int) -> int:
    heap = [(0, start)]
    visited = {}
    while heap:
        dist, node = heapq.heappop(heap)
        if node == end:
            return dist
        if node in visited:
            continue
        visited[node] = dist
        for neighbor, weight in graph.get(node, []):
            if neighbor not in visited:
                heapq.heappush(heap, (dist + weight, neighbor))
    return -1

# Topic: Longest Common Subsequence LCS dynamic programming
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

# Topic: Binary Tree DFS Traversal TreeNode Inorder
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

# Topic: 0/1 Knapsack Problem DP capacity
def knapsack(weights: List[int], values: List[int], capacity: int) -> int:
    n = len(weights)
    dp = [0] * (capacity + 1)
    for i in range(n):
        for w in range(capacity, weights[i] - 1, -1):
            dp[w] = max(dp[w], dp[w - weights[i]] + values[i])
    return dp[capacity]

# Topic: Levenshtein Edit Distance matrix operations
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

# Topic: Heapsort Algorithm min-heap sort array
def heapsort(arr: List[int]) -> List[int]:
    h = []
    for x in arr:
        heapq.heappush(h, x)
    return [heapq.heappop(h) for _ in range(len(arr))]

# Topic: Basic Regex Match wildcard star support
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

# Topic: Topological Sort DAG ordering
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

# Topic: Astar Search Pathfinder 2D Grid
def astar(grid: List[List[int]], start: Tuple[int, int], end: Tuple[int, int]) -> int:
    R, C = len(grid), len(grid[0])
    heap = [(0 + abs(start[0]-end[0])+abs(start[1]-end[1]), 0, start[0], start[1])]
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

    with open(os.path.join("reference_templates", "code_templates.py"), "w") as f:
        f.write(templates_content)

    # 4. Generate updated harness.py with all 20 tests
    harness_content = """import sys
import os
import importlib
import traceback

def run_tests():
    passed = 0
    total = 20

    sys.path.insert(0, os.path.abspath("tasks"))

    # Test 1: Anagram
    try:
        import task_1_anagram
        importlib.reload(task_1_anagram)
        assert task_1_anagram.is_anagram("listen", "silent") == True
        assert task_1_anagram.is_anagram("hello", "billion") == False
        passed += 1
    except Exception as e:
        print("FAIL Test 1 (Anagram) Failed:", e)

    # Test 2: LRU Cache
    try:
        import task_2_lru_cache
        importlib.reload(task_2_lru_cache)
        cache = task_2_lru_cache.LRUCache(2)
        cache.put(1, 1)
        cache.put(2, 2)
        assert cache.get(1) == 1
        cache.put(3, 3)
        assert cache.get(2) == -1
        passed += 1
    except Exception as e:
        print("FAIL Test 2 (LRU Cache) Failed:", e)

    # Test 3: Matrix Transpose
    try:
        import task_3_matrix_transpose
        importlib.reload(task_3_matrix_transpose)
        assert task_3_matrix_transpose.transpose([[1, 2], [3, 4]]) == [[1, 3], [2, 4]]
        passed += 1
    except Exception as e:
        print("FAIL Test 3 (Matrix Transpose) Failed:", e)

    # Test 4: Binary Search
    try:
        import task_4_binary_search
        importlib.reload(task_4_binary_search)
        assert task_4_binary_search.binary_search([1, 3, 5, 7], 5) == 2
        assert task_4_binary_search.binary_search([1, 3, 5, 7], 2) == -1
        passed += 1
    except Exception as e:
        print("FAIL Test 4 (Binary Search) Failed:", e)

    # Test 5: Merge Intervals
    try:
        import task_5_merge_intervals
        importlib.reload(task_5_merge_intervals)
        assert task_5_merge_intervals.merge_intervals([[1, 3], [2, 4]]) == [[1, 4]]
        passed += 1
    except Exception as e:
        print("FAIL Test 5 (Merge Intervals) Failed:", e)

    # Test 6: Trie Wildcard
    try:
        import task_6_trie_wildcard
        importlib.reload(task_6_trie_wildcard)
        trie = task_6_trie_wildcard.Trie()
        trie.insert("bad")
        assert trie.search(".ad") == True
        assert trie.search("b..") == True
        serialized = trie.serialize()
        trie2 = task_6_trie_wildcard.Trie.deserialize(serialized)
        assert trie2.search("bad") == True
        passed += 1
    except Exception as e:
        print("FAIL Test 6 (Trie Wildcard) Failed:", e)

    # Test 7: Red-Black Tree
    try:
        import task_7_red_black_tree
        importlib.reload(task_7_red_black_tree)
        RBT = task_7_red_black_tree.RedBlackTree()
        for v in [7, 3, 18, 10]:
            RBT.insert(v)
        
        # Test basic property check on root node
        assert RBT.root.color == False  # Root must be black
        passed += 1
    except Exception as e:
        print("FAIL Test 7 (Red-Black Tree) Failed:", e)

    # Test 8: Quicksort
    try:
        import task_8_quicksort
        importlib.reload(task_8_quicksort)
        assert task_8_quicksort.quicksort([4, 2, 7, 1]) == [1, 2, 4, 7]
        passed += 1
    except Exception as e:
        print("FAIL Test 8 (Quicksort) Failed:", e)

    # Test 9: Fibonacci
    try:
        import task_9_fibonacci
        importlib.reload(task_9_fibonacci)
        assert task_9_fibonacci.fib(10) == 55
        passed += 1
    except Exception as e:
        print("FAIL Test 9 (Fibonacci) Failed:", e)

    # Test 10: JSON Parser
    try:
        import task_10_json_parser
        importlib.reload(task_10_json_parser)
        assert task_10_json_parser.parse_json('{"a": [1, 2]}') == {"a": [1, 2]}
        passed += 1
    except Exception as e:
        print("FAIL Test 10 (JSON Parser) Failed:", e)

    # Test 11: String Compress
    try:
        import task_11_string_compress
        importlib.reload(task_11_string_compress)
        assert task_11_string_compress.compress_string("aabcccccaaa") == "a2b1c5a3"
        assert task_11_string_compress.compress_string("abc") == "abc"
        passed += 1
    except Exception as e:
        print("FAIL Test 11 (String Compress) Failed:", e)

    # Test 12: Dijkstra
    try:
        import task_12_dijkstra
        importlib.reload(task_12_dijkstra)
        g = {0: [(1, 2), (2, 4)], 1: [(2, 1)], 2: []}
        assert task_12_dijkstra.dijkstra(g, 0, 2) == 3
        passed += 1
    except Exception as e:
        print("FAIL Test 12 (Dijkstra) Failed:", e)

    # Test 13: LCS
    try:
        import task_13_lcs
        importlib.reload(task_13_lcs)
        assert task_13_lcs.lcs("abcde", "ace") == 3
        passed += 1
    except Exception as e:
        print("FAIL Test 13 (LCS) Failed:", e)

    # Test 14: Tree DFS
    try:
        import task_14_tree_dfs
        importlib.reload(task_14_tree_dfs)
        TN = task_14_tree_dfs.TreeNode
        r = TN(2)
        r.left = TN(1)
        r.right = TN(3)
        assert task_14_tree_dfs.dfs_inorder(r) == [1, 2, 3]
        passed += 1
    except Exception as e:
        print("FAIL Test 14 (Tree DFS) Failed:", e)

    # Test 15: Knapsack
    try:
        import task_15_knapsack
        importlib.reload(task_15_knapsack)
        assert task_15_knapsack.knapsack([1, 2, 3], [6, 10, 12], 5) == 22
        passed += 1
    except Exception as e:
        print("FAIL Test 15 (Knapsack) Failed:", e)

    # Test 16: Levenshtein
    try:
        import task_16_levenshtein
        importlib.reload(task_16_levenshtein)
        assert task_16_levenshtein.edit_distance("horse", "ros") == 3
        passed += 1
    except Exception as e:
        print("FAIL Test 16 (Levenshtein) Failed:", e)

    # Test 17: Heapsort
    try:
        import task_17_heapsort
        importlib.reload(task_17_heapsort)
        assert task_17_heapsort.heapsort([3, 1, 4, 1]) == [1, 1, 3, 4]
        passed += 1
    except Exception as e:
        print("FAIL Test 17 (Heapsort) Failed:", e)

    # Test 18: Regex Match
    try:
        import task_18_regex_match
        importlib.reload(task_18_regex_match)
        assert task_18_regex_match.is_match("aa", "a*") == True
        assert task_18_regex_match.is_match("ab", ".*") == True
        passed += 1
    except Exception as e:
        print("FAIL Test 18 (Regex Match) Failed:", e)

    # Test 19: Topological Sort
    try:
        import task_19_topological_sort
        importlib.reload(task_19_topological_sort)
        assert task_19_topological_sort.topological_sort(3, [[0, 1], [1, 2]]) == [0, 1, 2]
        passed += 1
    except Exception as e:
        print("FAIL Test 19 (Topological Sort) Failed:", e)

    # Test 20: Astar
    try:
        import task_20_astar
        importlib.reload(task_20_astar)
        grid = [[0, 0, 0], [0, 1, 0], [0, 0, 0]]
        assert task_20_astar.astar(grid, (0, 0), (2, 2)) == 4
        passed += 1
    except Exception as e:
        print("FAIL Test 20 (Astar) Failed:", e)

    print(f"\\nScore: {passed}/{total} ({passed/total*100:.1f}%)")

if __name__ == "__main__":
    run_tests()
"""

    with open("harness.py", "w") as f:
        f.write(harness_content)
    
    print("OK Successfully initialized 20 benchmark tasks!")

if __name__ == "__main__":
    setup()
