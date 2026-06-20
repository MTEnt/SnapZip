class CacheStore:
    def __init__(self):
        self._values = {}

    def put(self, key, value):
        self._values[key] = value

    def get(self, key, default=None):
        return self._values.get(key, default)


def build_cache(seed="ready"):
    cache = CacheStore()
    cache.put("seed", seed)
    return cache
