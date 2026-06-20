from collections import OrderedDict
from typing import Optional


class BoundedCache:
    def __init__(self, capacity: int):
        if capacity <= 0:
            raise ValueError("capacity must be positive")
        self.capacity = capacity
        self._items: OrderedDict[str, str] = OrderedDict()

    def get(self, key: str) -> Optional[str]:
        if key not in self._items:
            return None
        value = self._items.pop(key)
        self._items[key] = value
        return value

    def put(self, key: str, value: str) -> None:
        if key in self._items:
            self._items.pop(key)
        self._items[key] = value
        while len(self._items) > self.capacity:
            self._items.popitem(last=False)
