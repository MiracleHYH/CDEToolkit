#!/usr/bin/env python3
"""Bounded gzip decompression for downloaded mihomo release assets."""

from __future__ import annotations

import argparse
import gzip
import os
from pathlib import Path
import tempfile


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--max-bytes", required=True, type=int)
    args = parser.parse_args()
    if args.max_bytes <= 0:
        raise SystemExit("max-bytes must be positive")

    written = 0
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb", prefix=f".{args.output.name}.", dir=args.output.parent, delete=False
        ) as target:
            temporary_path = Path(target.name)
            with gzip.open(args.input, "rb") as source:
                while chunk := source.read(
                    min(1024 * 1024, args.max_bytes - written + 1)
                ):
                    written += len(chunk)
                    if written > args.max_bytes:
                        raise SystemExit("decompressed core exceeds size limit")
                    target.write(chunk)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary_path, args.output)
        temporary_path = None
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
