"""增量 JSONL 轨迹写入器。

每行一个 JSON 对象(带 ts/round/type),写完立即 flush:
进程被 kill、超时 cancel、Ctrl-C 都不丢已写内容。
AdapterClient 在 worker 线程、agent 在事件循环线程都会写,故加锁。
"""

from __future__ import annotations

import json
import threading
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


class EventLogger:
    """线程安全的追加式 JSONL 记录器。"""

    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        # 行缓冲:每行写完即对进程外可见
        self._fp = open(self.path, "a", encoding="utf-8", buffering=1)
        self._lock = threading.Lock()
        self._closed = False

    def log(self, event_type: str, round_no: Optional[int] = None, **fields: Any) -> None:
        if self._closed:
            return
        record = {
            "ts": datetime.now(timezone.utc).isoformat(),
            "type": event_type,
            **({"round": round_no} if round_no is not None else {}),
            **fields,
        }
        line = json.dumps(record, ensure_ascii=False, default=str)
        with self._lock:
            if self._closed:
                return
            try:
                self._fp.write(line + "\n")
                self._fp.flush()
            except Exception:
                # 轨迹写入失败不应影响评测主流程
                pass

    def close(self) -> None:
        with self._lock:
            if not self._closed:
                self._closed = True
                try:
                    self._fp.close()
                except Exception:
                    pass

    def __enter__(self) -> "EventLogger":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()
