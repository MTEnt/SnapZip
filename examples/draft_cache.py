from collections import OrderedDict


class RecentValueCache:
    def __init__(self, capacity: int):
        self.capacity = capacity
        self.items = OrderedDict()

    def get(self, key: str):
        if key not in self.items:
            return None
        value = self.items.pop(key)
        self.items[key] = value
        return value

    def put(self, key: str, value: str) -> None:
        if key in self.items:
            self.items.pop(key)
        self.items[key] = value
        if len(self.items) > self.capacity:
            self.items.popitem(last=False)
