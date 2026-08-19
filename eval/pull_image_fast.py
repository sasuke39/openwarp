#!/usr/bin/env python3
"""多连接并行拉取 Docker Hub 镜像并 docker load。

背景:Docker Hub 直连不可达,dockerproxy.net 可用但单连接限速 ~100KB/s。
本脚本对每层做 HTTP Range 分块并行下载(默认 16 连接),组装成
docker save 格式的 tar,再用 `docker load` 导入并打上原始 tag。

用法: pull_image_fast.py <repo:tag> [--mirror dockerproxy.net] [--conn 16]
示例: pull_image_fast.py alexgshaw/path-tracing:20251031
"""
import argparse
import concurrent.futures as cf
import io
import json
import os
import re
import sys
import tarfile
import tempfile
import time
import urllib.request

CHUNK = 8 * 1024 * 1024  # 每块 8MB


def http_get(url, headers=None, timeout=30):
    req = urllib.request.Request(url, headers=headers or {})
    return urllib.request.urlopen(req, timeout=timeout)


def get_token(mirror, repo):
    url = f"https://{mirror}/v2/token?scope=repository:{repo}:pull&service=registry"
    try:
        data = http_get(url).read().decode("utf-8", "replace")
        m = re.search(r'"token"\s*:\s*"([^"]+)"', data)
        return m.group(1) if m else ""
    except Exception:
        return ""  # 部分镜像站匿名可直拉,无 token 也可


def get_manifest(mirror, repo, tag, token):
    url = f"https://{mirror}/v2/{repo}/manifests/{tag}"
    headers = {"Accept": "application/vnd.docker.distribution.manifest.v2+json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return json.loads(http_get(url, headers).read())


def fetch_range(mirror, repo, digest, start, end, token, retries=4):
    url = f"https://{mirror}/v2/{repo}/blobs/{digest}"
    for attempt in range(retries):
        try:
            headers = {"Range": f"bytes={start}-{end}"}
            if token:
                headers["Authorization"] = f"Bearer {token}"
            return start, http_get(url, headers, timeout=120).read()
        except Exception:
            if attempt == retries - 1:
                raise
            time.sleep(1.5 * (attempt + 1))


def download_blob(mirror, repo, digest, size, token, conn, pool, tag=""):
    parts = {}
    ranges = [(s, min(s + CHUNK, size) - 1) for s in range(0, size, CHUNK)]
    t0 = time.time()
    done = 0
    futs = [pool.submit(fetch_range, mirror, repo, digest, s, e, token) for s, e in ranges]
    for fut in cf.as_completed(futs):
        start, data = fut.result()
        parts[start] = data
        done += len(data)
        spd = done / max(time.time() - t0, 0.01) / 1e6
        print(f"  {tag} {done/1e6:.0f}/{size/1e6:.0f}MB {spd:.1f}MB/s", flush=True)
    buf = io.BytesIO()
    for s in sorted(parts):
        buf.write(parts[s])
    return buf.getvalue()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("image", help="repo:tag,如 alexgshaw/path-tracing:20251031")
    ap.add_argument("--mirror", default="dockerproxy.net")
    ap.add_argument("--conn", type=int, default=16)
    args = ap.parse_args()
    repo, tag = args.image.rsplit(":", 1)

    token = get_token(args.mirror, repo)
    manifest = get_manifest(args.mirror, repo, tag, token)
    cfg_digest = manifest["config"]["digest"]
    layers = manifest["layers"]
    total = sum(l["size"] for l in layers) + manifest["config"]["size"]
    print(f"{args.image}: {len(layers)} 层,共 {total/1e9:.2f}GB,{args.conn} 连接", flush=True)

    t0 = time.time()
    with cf.ThreadPoolExecutor(max_workers=args.conn) as pool:
        cfg = download_blob(args.mirror, repo, cfg_digest, manifest["config"]["size"], token, args.conn, pool, "config")
        layer_blobs = []
        for i, l in enumerate(layers):
            blob = download_blob(args.mirror, repo, l["digest"], l["size"], token, args.conn, pool, f"layer{i+1}/{len(layers)}")
            layer_blobs.append((l["digest"], blob))

    # 组装 docker save 格式 tar
    def hexid(d):
        return d.split(":", 1)[1]

    layer_names = [f"{hexid(d)}/layer.tar" for d, _ in layer_blobs]
    cfg_name = f"{hexid(cfg_digest)}.json"
    mf = [{"Config": cfg_name, "RepoTags": [args.image], "Layers": layer_names}]

    out = tempfile.NamedTemporaryFile(prefix="tb2img_", suffix=".tar", delete=False)
    try:
        with tarfile.open(fileobj=out, mode="w") as tar:
            def add_bytes(name, data):
                info = tarfile.TarInfo(name)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            for (d, blob), name in zip(layer_blobs, layer_names):
                sub = tarfile.TarInfo(f"{hexid(d)}/")
                sub.type = tarfile.DIRTYPE
                tar.addfile(sub)
                add_bytes(name, blob)
                add_bytes(f"{hexid(d)}/json", json.dumps({"id": hexid(d)}).encode())
            add_bytes(cfg_name, cfg)
            add_bytes("manifest.json", json.dumps(mf).encode())
            add_bytes("repositories", json.dumps({repo.rsplit("/", 1)[-1] if "/" in repo else repo: {tag: hexid(layer_blobs[-1][0])}}).encode())
    except Exception:
        os.unlink(out.name)
        raise

    rc = os.system(f"docker load -i {out.name}")
    os.unlink(out.name)
    if rc != 0:
        print("docker load 失败", file=sys.stderr)
        sys.exit(1)
    print(f"完成 {args.image},耗时 {time.time()-t0:.0f}s", flush=True)


if __name__ == "__main__":
    main()
