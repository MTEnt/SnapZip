from app.cache import CacheStore, build_cache


def test_build_cache_sets_seed():
    cache = build_cache("ship")
    assert cache.get("seed") == "ship"


def test_cache_default_value():
    cache = CacheStore()
    assert cache.get("missing", "fallback") == "fallback"
