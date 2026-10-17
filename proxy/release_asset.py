#!/usr/bin/env python3
"""Select the matching mihomo release asset from GitHub release JSON."""

import json
import re
import sys


def fail(message: str) -> "None":
    print(message, file=sys.stderr)
    raise SystemExit(1)


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: release_asset.py ARCH")
    arch = sys.argv[1]
    suffixes = {
        "x86_64": ["amd64-compatible", "amd64"],
        "amd64": ["amd64-compatible", "amd64"],
        "aarch64": ["arm64"],
        "arm64": ["arm64"],
        "armv7l": ["armv7"],
        "armv7": ["armv7"],
        "i386": ["386"],
        "i686": ["386"],
    }.get(arch)
    if suffixes is None:
        fail(f"unsupported architecture: {arch}")

    try:
        release = json.load(sys.stdin)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        fail(f"invalid GitHub release JSON: {exc}")

    tag = release.get("tag_name")
    assets = release.get("assets")
    if not isinstance(tag, str) or not tag or not isinstance(assets, list):
        fail("release JSON is missing tag_name or assets")

    by_name = {
        asset.get("name"): asset
        for asset in assets
        if isinstance(asset, dict) and isinstance(asset.get("name"), str)
    }
    selected = None
    for suffix in suffixes:
        selected = by_name.get(f"mihomo-linux-{suffix}-{tag}.gz")
        if selected is not None:
            break
    if selected is None:
        fail(f"no compatible mihomo asset for {arch} in {tag}")

    url = selected.get("browser_download_url")
    digest = selected.get("digest")
    if not isinstance(url, str) or not url.startswith("https://github.com/"):
        fail("selected asset has an invalid download URL")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-fA-F]{64}", digest):
        fail("selected asset is missing a valid SHA-256 digest")

    print(url)
    print(digest.lower())


if __name__ == "__main__":
    main()
