#!/usr/bin/env python3
"""Adopt a relocated standalone install without starting Codex or changing auth."""

import argparse
import json
import os
from pathlib import Path
import re
import tempfile


def executable(path):
    return path.is_file() and os.access(path, os.X_OK)


def release_target(home, package):
    base = home / "packages" / package
    current = base / "current"
    if current.is_symlink():
        hint = Path(os.readlink(current)).name
        candidate = base / "releases" / hint
        if executable(candidate / "bin/codex") and (candidate / "bin/codex").resolve().is_relative_to(home.resolve()):
            return candidate
    if executable(current / "bin/codex") and (current / "bin/codex").resolve().is_relative_to(home.resolve()):
        return current.resolve()
    if package != "standalone":
        return None
    candidates = [p for p in (base / "releases").glob("*")
                  if executable(p / "bin/codex") and (p / "bin/codex").resolve().is_relative_to(home.resolve())]
    return max(candidates, key=lambda p: (tuple(map(int, re.findall(r"\d+", p.name))), p.name), default=None)


def read_private(path):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise ValueError(f"Expected a regular file: {path}")
    return path.read_text() if path.exists() else None


def strip_retired_block(text):
    begin = "# >>> server-toolkit volcml-cowork >>>"
    end = "# <<< server-toolkit volcml-cowork <<<"
    lines = text.splitlines(keepends=True)
    starts = [i for i, line in enumerate(lines) if line.rstrip("\r\n") == begin]
    ends = [i for i, line in enumerate(lines) if line.rstrip("\r\n") == end]
    if not starts and not ends:
        return text
    if len(starts) != 1 or len(ends) != 1 or starts[0] >= ends[0]:
        raise ValueError("Malformed retired instruction block; refusing to change it")
    return "".join(lines[:starts[0]] + lines[ends[0] + 1:])


def migrate_config(text, home, toolkit, projects):
    if not projects:
        return text
    mappings = []
    for prefix in ("/workspace/.local", "/project_c20250514/250307/.local"):
        mappings.extend(((prefix + "/.codex", str(home)),
                         (prefix + "/server-toolkit", str(toolkit)),
                         (prefix + "/proxy", str(toolkit / "proxy"))))
    mappings.append(("/workspace/proj", projects))

    def replace(match):
        token = match[0]
        try:
            value = json.loads(token)
        except ValueError:
            return token
        for old, new in mappings:
            if value == old or value.startswith(old + "/"):
                return json.dumps(new + value[len(old):], ensure_ascii=False)
        return token

    return re.sub(r'"(?:[^"\\]|\\.)*"', replace, text)


def atomic_text(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def validate_parents(path, home):
    for parent in path.parents:
        if parent == home:
            return
        if parent.is_symlink() or (parent.exists() and not parent.is_dir()):
            raise ValueError(f"Unsafe parent directory: {parent}")
    raise ValueError(f"Path is outside CODEX_HOME: {path}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", required=True, type=Path)
    parser.add_argument("--toolkit", required=True, type=Path)
    parser.add_argument("--projects-root", default="")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    home = args.home
    if not home.is_absolute() or home.is_symlink() or (home.exists() and not home.is_dir()):
        raise ValueError("CODEX_HOME must be an absolute, regular directory")
    standalone = release_target(home, "standalone")
    if standalone is None and not executable(home / "bin/codex"):
        return 3
    if args.check:
        return 0

    links = []
    if standalone is not None:
        current = home / "packages/standalone/current"
        if not current.is_symlink() and current.exists():
            raise ValueError(f"Refusing to replace a regular directory: {current}")
        links.append((current, os.path.relpath(standalone, current.parent)))
        command = home / "bin/codex"
        if command.is_symlink() or not command.exists():
            links.append((command, "../packages/standalone/current/bin/codex"))
        legacy = home / "packages/server-toolkit/bin/codex"
        if legacy.is_symlink():
            links.append((legacy, "../../standalone/current/bin/codex"))
    daemon = release_target(home, "app-server-daemon")
    daemon_current = home / "packages/app-server-daemon/current"
    if daemon is not None and daemon_current.is_symlink():
        links.append((daemon_current, os.path.relpath(daemon, daemon_current.parent)))

    # Prepare every text change before applying any of them. Never open auth,
    # sessions, SQLite databases, account stores, or experiment files.
    texts = []
    config = home / "config.toml"
    text = read_private(config)
    if text is not None:
        migrated = migrate_config(text, home, args.toolkit, args.projects_root)
        if migrated != text:
            try:
                import tomllib
            except ImportError:
                pass
            else:
                tomllib.loads(migrated)
            texts.append((config, migrated))
    for name in ("AGENTS.md", "AGENTS.override.md"):
        path = home / name
        text = read_private(path)
        if text is not None:
            cleaned = strip_retired_block(text)
            if cleaned != text:
                texts.append((path, cleaned))

    retired_skill = home / "skills/volcml-cowork"
    for path in [p for p, _ in links] + [p for p, _ in texts] + [retired_skill]:
        validate_parents(path, home)
    for path, target in links:
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.is_symlink() and os.readlink(path) == target:
            continue
        temporary = path.with_name(path.name + f".stk-{os.getpid()}")
        try:
            temporary.symlink_to(target)
            os.replace(temporary, path)
        finally:
            if temporary.is_symlink():
                temporary.unlink()
    for path, text in texts:
        atomic_text(path, text)
    if retired_skill.is_symlink() and os.readlink(retired_skill).endswith("/modules/volcml/skills/volcml-cowork"):
        retired_skill.unlink()
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError) as error:
        raise SystemExit(str(error))
