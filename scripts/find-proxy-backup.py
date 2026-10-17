#!/usr/bin/env python3
"""Find a validated private proxy snapshot; print paths and listener names only."""

import argparse
from pathlib import Path
import re
import yaml


def regular(path):
    return path.is_file() and not path.is_symlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("toolkit", type=Path)
    args = parser.parse_args()
    base = args.toolkit / ".transactions"
    if base.is_symlink():
        return 3
    snapshots = [p for p in base.glob("transaction.*") if p.is_dir() and not p.is_symlink()]
    for snapshot in sorted(snapshots, key=lambda p: p.stat().st_mtime, reverse=True):
        proxy = None
        settings = {}
        for record in snapshot.glob("*.path"):
            data = record.with_suffix(".data")
            kind = record.with_suffix(".kind")
            if not all(regular(p) for p in (record, data, kind)) or kind.read_text().strip() != "present":
                continue
            target = record.read_text().strip()
            if target.endswith("/server-toolkit/proxy.yaml"):
                proxy = data
            elif target.endswith("/server-toolkit/config.conf"):
                settings = dict(line.split("=", 1) for line in data.read_text().splitlines()
                                if "=" in line and not line.startswith("#"))
        if proxy is None:
            continue
        try:
            config = yaml.safe_load(proxy.read_text())
            listeners = config["listeners"]
            names = {item["name"] for item in listeners}
            common = settings.get("proxy_common_listener", "common")
            codex = settings.get("proxy_codex_listener", common)
            if not all(re.fullmatch(r"[A-Za-z0-9._/-]+", name) and name in names for name in (common, codex)):
                continue
            if config.get("allow-lan", False) or not all(
                    item.get("listen") in ("127.0.0.1", "::1", "localhost") and
                    item.get("type") == "mixed" and 1 <= int(item["port"]) <= 65535 for item in listeners):
                continue
        except (KeyError, TypeError, ValueError, yaml.YAMLError):
            continue
        print(proxy)
        print(common)
        print(codex)
        return 0
    return 3


if __name__ == "__main__":
    raise SystemExit(main())
