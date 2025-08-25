import threading
from collections import defaultdict
from typing import Dict


class ResourceTracker:
    def __init__(self):
        self._resources = defaultdict(dict)
        self._lock = threading.Lock()

    def track(self, sandbox_id: str, resource_type: str, resource_id: str):
        with self._lock:
            self._resources[sandbox_id][resource_type] = resource_id

    def release(self, sandbox_id: str) -> Dict[str, str]:
        with self._lock:
            return self._resources.pop(sandbox_id, {})

    def get_resources(self, sandbox_id: str) -> Dict[str, str]:
        with self._lock:
            return self._resources.get(sandbox_id, {}).copy()
