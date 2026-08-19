"""protoc 生成的 stub 包。

`protoc --python_out` 生成的是平铺 import(如 `import task_pb2`),不是包内
相对 import。这里把本目录加入 sys.path,让这些平铺 import 在包内也能解析。
stub 由 ../gen_stubs.sh 生成,除本文件外均为生成产物。
"""

import pathlib
import sys

_dir = str(pathlib.Path(__file__).resolve().parent)
if _dir not in sys.path:
    sys.path.insert(0, _dir)
