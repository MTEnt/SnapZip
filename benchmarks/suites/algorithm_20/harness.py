import sys
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

    print(f"\nScore: {passed}/{total} ({passed/total*100:.1f}%)")

if __name__ == "__main__":
    run_tests()
