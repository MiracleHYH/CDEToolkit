#!/usr/bin/env python3
"""Offline P0 controller; run only through conda-p0-isolation.sh.

Uses the exact Miniconda Python inside a disposable container. No host Python,
package operation, solver, activation hook or external network is needed.
"""

from __future__ import annotations

import argparse
import configparser
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import time
import tomllib
import traceback


MIRROR_KEYS = (
    "channels", "default_channels", "custom_channels", "custom_multichannels",
    "channel_alias", "migrated_channel_aliases", "migrated_custom_channels",
)
OTHER_KEYS = ("show_channel_urls", "ssl_verify", "proxy_servers")
PREFIX = Path("/runtime/miniconda")
CONDA = str(PREFIX / "bin/conda")
PYTHON = str(PREFIX / "bin/python")
ORIGINAL_PIP = "https://mirrors.ivolces.com/pypi/simple/"
TARGET_CONDARC = """# Target-like original configuration; preserve this comment.
channels:
  - defaults
show_channel_urls: true
default_channels:
  - https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main
  - https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/r
  - https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/msys2
custom_channels:
  conda-forge: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  msys2: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  bioconda: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  menpo: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  pytorch: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  pytorch-lts: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  simpleitk: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
  nvidia: https://mirrors.sustech.edu.cn/anaconda-extra/cloud
"""


def equal(actual: object, expected: object, message: str) -> None:
    if actual != expected:
        raise AssertionError(f"{message}\nexpected={expected!r}\nactual={actual!r}")


def write(path: Path, text: str, mode: int = 0o644) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    path.chmod(mode)


def digest_tree(directory: Path) -> dict[str, object]:
    result = {}
    for path in sorted(directory.rglob("*")):
        info = path.lstat()
        value = [stat.S_IFMT(info.st_mode), stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid]
        if path.is_symlink():
            value.append(os.readlink(path))
        elif path.is_file():
            value.append(hashlib.sha256(path.read_bytes()).hexdigest())
        result[str(path.relative_to(directory))] = value
    return result


class Acceptance:
    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.output = Path(args.output)
        self.output.mkdir(parents=True, exist_ok=True)
        self.sequence = 0
        self.commands = self.output / "commands.jsonl"
        self.results: list[dict[str, object]] = []
        self.validation_failures: list[dict[str, object]] = []
        self.nido_version: str | None = None
        self.base = {
            "PATH": f"{PREFIX}/bin:/usr/bin:/bin", "HOME": "/work/bootstrap",
            "TMPDIR": "/work/tmp", "CONDA_EXE": CONDA,
            "CONDA_OFFLINE": "true", "CONDA_NO_PLUGINS": "true",
            "CONDA_REPORT_ERRORS": "false", "PYTHONDONTWRITEBYTECODE": "1",
            "PIP_DISABLE_PIP_VERSION_CHECK": "1", "BASH_ENV": "/dev/null", "ENV": "/dev/null",
            "LC_ALL": "C.UTF-8",
        }
        Path(self.base["HOME"]).mkdir(parents=True)
        Path(self.base["TMPDIR"]).mkdir(parents=True)

    def run(self, argv: list[str], env: dict[str, str] | None = None, *, fail: bool = False, timeout: int = 120) -> str:
        self.sequence += 1
        started = time.monotonic()
        proc = subprocess.run(argv, env=self.base if env is None else env,
                              capture_output=True, text=True, timeout=timeout)
        record = {"id": self.sequence, "argv": argv, "exit": proc.returncode,
                  "seconds": round(time.monotonic() - started, 3),
                  "stdout": proc.stdout, "stderr": proc.stderr}
        with self.commands.open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(record, ensure_ascii=False) + "\n")
        if fail:
            if proc.returncode == 0:
                raise AssertionError(f"unexpected success: {argv!r}\n{proc.stdout}")
        elif proc.returncode:
            raise AssertionError(f"command failed: {record!r}")
        return proc.stdout

    def group(self, name: str, function) -> None:
        print(f"START {name}", flush=True)
        try:
            function()
            self.results.append({"name": name, "passed": True})
            print(f"PASS {name}", flush=True)
        except Exception:
            failure = traceback.format_exc()
            self.results.append({"name": name, "passed": False, "failure": failure})
            print(f"FAIL {name}\n{failure}", flush=True)

    def identity(self) -> None:
        equal(sys.version_info[:3], (3, 12, 1), "controller Python version")
        equal(sys.prefix, str(PREFIX), "controller must use isolated prefix")
        equal(self.run([CONDA, "--version"]).strip(), "conda 24.1.2", "Conda version")
        pip = self.run([PYTHON, "-m", "pip", "--version"]).strip()
        assert pip.startswith("pip 23.3.1 from /runtime/miniconda/"), pip
        version = json.loads(self.run([self.args.binary, "version", "--json"]))
        if not isinstance(version, dict) or not isinstance(version.get("version"), str) or not version["version"]:
            raise AssertionError(f"invalid Nido version response: {version!r}")
        self.nido_version = version["version"]
        if self.args.expected_version:
            equal(self.nido_version, self.args.expected_version, "Nido version")
        # Check the kernel boundary itself; do not monkeypatch upstream probes.
        history = PREFIX / "conda-meta/history"
        original = hashlib.sha256(history.read_bytes()).hexdigest()
        try:
            with history.open("a+"):
                pass
            writable = True
        except OSError as error:
            writable = False
            denied = {"errno": error.errno, "reason": str(error)}
        equal(writable, self.args.mode == "normal", "mode did not enforce prefix writability")
        equal(hashlib.sha256(history.read_bytes()).hexdigest(), original, "write-intent probe changed history")
        probe = {"mode": self.args.mode, "history_a_plus_succeeded": writable,
                 "history_unchanged": True, "pip_version": pip}
        if not writable:
            probe["denial"] = denied
        (self.output / "runtime-probe.json").write_text(json.dumps(probe, indent=2) + "\n")

    def target(self) -> None:
        base = Path("/work/target")
        home, root, data = base / "home", base / "nido", base / "data"
        home.mkdir(parents=True)
        home.chmod(0o700)
        source = home / ".condarc"
        legacy, modern = home / ".pip/pip.conf", home / ".config/pip/pip.conf"
        write(source, TARGET_CONDARC)
        write(legacy, "[global]\nindex-url=" + ORIGINAL_PIP + "\n")
        write(modern, "[global]\nindex-url = " + ORIGINAL_PIP + "\n")
        write(data / "conda_envs/energy/conda-meta/history", "# fixture existing environment\n")
        write(data / "conda_envs/energy/conda-meta/state",
              '{"env_vars":{"CONDA_PKGS_DIRS":"/workspace/.local/conda_pkgs"}}\n')
        write(data / "conda_pkgs/keep.txt", "EXISTING CONDA CACHE\n")
        write(data / "pip_cache/keep.txt", "EXISTING PIP CACHE\n")
        broken = data / "conda_envs/diffdock-kernels/bin/python"
        broken.parent.mkdir(parents=True)
        broken.symlink_to("/workspace/.local/conda_envs/SPARCDock/bin/python")
        sentinels = digest_tree(data)
        env = dict(self.base, HOME=str(home), NIDO_HOME=str(root))
        assert "CONDARC" not in env and "PIP_CONFIG_FILE" not in env

        def nido(*args: str, current=None, fail=False) -> str:
            return self.run([self.args.binary, "--root", str(root), *args],
                            env if current is None else current, fail=fail)

        def show(keys, current=None):
            return json.loads(self.run([CONDA, "config", "--show", *keys, "--json"],
                                       env if current is None else current))

        def cold():
            # Every call starts with original ENV, not markers from a prior shell.
            output = self.run(["/bin/bash", "--noprofile", "--norc", "-c",
                               '. "$1"; env -0', "p0-cold", str(root / "env.sh")], env)
            return dict(item.split("=", 1) for item in output.split("\0") if item)

        def snapshot():
            return tomllib.loads((root / "config.toml").read_text())["conda"]["defaults"]

        def validate(current):
            try:
                nido("conda", "validate", "--json", current=current)
            except AssertionError as error:
                # Finish the storage/default matrix even if an earlier preset
                # is broken. The group must still fail at its final assertion.
                actual = show(MIRROR_KEYS, current)
                self.validation_failures.append({"error": str(error), "actual_mirrors": actual})
                (self.output / "validation-failures.json").write_text(
                    json.dumps(self.validation_failures, indent=2) + "\n")

        original_mirrors = show(MIRROR_KEYS)
        original_other = show(OTHER_KEYS)
        baseline_dirs = show(("envs_dirs", "pkgs_dirs"))
        baseline_pip = self.run([PYTHON, "-m", "pip", "config", "list"], env)
        sources = json.loads(self.run([CONDA, "config", "--show-sources", "--json"], env))
        assert str(source) in sources, sources
        for key in ("envs_dirs", "pkgs_dirs", "custom_multichannels"):
            assert key not in sources[str(source)], key
        (self.output / "target-baseline.json").write_text(json.dumps({
            "mirrors": original_mirrors, "other": original_other,
            "directories": baseline_dirs, "sources": sources, "pip": baseline_pip,
            "pip_cache": self.run([PYTHON, "-m", "pip", "cache", "dir"], env).strip(),
        }, indent=2) + "\n")
        native_original = {p: p.read_bytes() for p in (source, legacy, modern)}
        nido("init", "--yes")
        nido("conda", "setup", "all", "--dry-run")
        assert not snapshot().get("conda_captured", False)
        equal({p: p.read_bytes() for p in native_original}, native_original, "dry-run modified native files")
        nido("conda", "setup", "conda", "--yes")
        assert snapshot()["conda_captured"] and not snapshot().get("pip_captured", False)
        conda_snapshot = snapshot().copy()
        nido("conda", "setup", "pip", "--yes")
        defaults = snapshot()
        equal(defaults["pip_index_url"], ORIGINAL_PIP, "pip original default lost")
        equal(defaults["conda_yaml"], conda_snapshot["conda_yaml"], "independent setup changed Conda default")
        nido("conda", "setup", "all", "--yes")
        equal(snapshot(), defaults, "repeated setup changed defaults")
        equal({p: p.read_bytes() for p in native_original}, native_original, "setup changed native files")
        for selection in ("official", "tuna"):
            before = digest_tree(home), digest_tree(root)
            nido("conda", "mirror", "conda", selection, "--dry-run")
            equal((digest_tree(home), digest_tree(root)), before, "preview changed files")
            nido("conda", "mirror", "conda", selection, "--yes")
            nido("conda", "mirror", "pip", selection, "--yes")
            active = cold()
            validate(active)
            equal(show(OTHER_KEYS, active), original_other, "mirror changed non-mirror settings")
            equal(show(("envs_dirs", "pkgs_dirs"), active), baseline_dirs, "mirror changed native directory semantics")
            equal(snapshot(), defaults, "switch overwrote default")
            assert "# Target-like original" in source.read_text()
            equal((source.stat().st_mode & 0o777, source.stat().st_gid), (0o644, os.getgid()), "source metadata changed")

        # Independent native storage updates start with all directory keys absent.
        targets = {"envs": data / "future envs", "pkgs": data / "future pkgs", "pip": data / "future pip"}
        for args, key in ((["envs"], "envs"), (["cache", "conda"], "pkgs"), (["cache", "pip"], "pip")):
            nido("conda", *args, str(targets[key]), "--yes")
        active = cold()
        for key in ("CONDA_ENVS_PATH", "CONDA_PKGS_DIRS", "PIP_CACHE_DIR"):
            assert key not in active, (key, active.get(key))
        actual_dirs = show(("envs_dirs", "pkgs_dirs"), active)
        equal(actual_dirs["envs_dirs"][0], str(targets["envs"]), "native envs first choice")
        equal(actual_dirs["pkgs_dirs"][0], str(targets["pkgs"]), "native cache first choice")
        equal(self.run([PYTHON, "-m", "pip", "cache", "dir"], env).strip(), str(targets["pip"]), "native pip cache")
        validate(active)
        equal(legacy.read_bytes(), native_original[legacy], "legacy pip file unnecessarily modified")
        parser = configparser.RawConfigParser()
        parser.read(modern)
        equal(parser.get("global", "index-url"), ORIGINAL_PIP, "cache changed original pip index")
        # The proposed server envs directory already contains environments.
        # Exercise existing data destinations as well as absent destinations.
        existing = {"envs": data / "conda_envs", "pkgs": data / "conda_pkgs", "pip": data / "pip_cache"}
        for args, key in ((["envs"], "envs"), (["cache", "conda"], "pkgs"), (["cache", "pip"], "pip")):
            nido("conda", *args, str(existing[key]), "--yes")
        existing_dirs = show(("envs_dirs", "pkgs_dirs"))
        equal(existing_dirs["envs_dirs"][0], str(existing["envs"]), "existing native envs destination")
        equal(existing_dirs["pkgs_dirs"][0], str(existing["pkgs"]), "existing native cache destination")
        equal(self.run([PYTHON, "-m", "pip", "cache", "dir"], env).strip(), str(existing["pip"]), "existing native pip cache destination")
        equal(digest_tree(data), sentinels, "setting existing directories changed their data")
        for variable, args in (("CONDA_ENVS_PATH", ["envs"]), ("CONDA_PKGS_DIRS", ["cache", "conda"]), ("PIP_CACHE_DIR", ["cache", "pip"])):
            before = digest_tree(home), digest_tree(root)
            nido("conda", *args, str(data / "blocked"), "--yes",
                 current=dict(env, **{variable: "/workspace/.local/override"}), fail=True)
            equal((digest_tree(home), digest_tree(root)), before, "conflicting override partially saved")
        for tool in ("conda", "pip"):
            nido("conda", "mirror", tool, "default", "--yes")
        active = cold()
        equal(show(MIRROR_KEYS, active), original_mirrors, "cold default changed original mirror semantics")
        equal(active["PIP_INDEX_URL"], ORIGINAL_PIP, "cold pip default")
        validate(active)
        saved_native = {p: p.read_bytes() for p in native_original}
        for args in (("mirror", "conda"), ("mirror", "pip"), ("envs",), ("cache", "conda"), ("cache", "pip")):
            nido("conda", *args, "off", "--yes")
        nido("conda", "reset", "--yes")
        equal({p: p.read_bytes() for p in native_original}, saved_native, "off/reset rolled native configuration back")
        equal(snapshot(), defaults, "reset deleted persistent defaults")
        nido("conda", "setup", "all", "--yes")
        equal(snapshot(), defaults, "setup after reset recaptured defaults")
        for tool in ("conda", "pip"):
            nido("conda", "mirror", tool, "default", "--yes")
        validate(cold())
        for target in targets.values():
            assert not target.exists(), f"query/setter created data directory: {target}"
        equal(digest_tree(data), sentinels, "existing environments/cache/broken links changed")
        assert not self.validation_failures, "upstream validation failed; see validation-failures.json"

    def pip_precedence(self) -> None:
        home = Path("/work/pip-precedence/home")
        root = home.parent / "nido"
        write(home / ".pip/pip.conf", "[global]\nindex-url=https://legacy.example.invalid/simple/\n")
        write(home / ".config/pip/pip.conf", "[global]\nindex-url=https://modern.example.invalid/simple/\n")
        env = dict(self.base, HOME=str(home))
        self.run([self.args.binary, "--root", str(root), "init", "--yes"], env)
        self.run([self.args.binary, "--root", str(root), "conda", "setup", "pip", "--yes"], env)
        defaults = tomllib.loads((root / "config.toml").read_text())["conda"]["defaults"]
        equal(defaults["pip_index_url"], "https://modern.example.invalid/simple/", "modern pip config precedence")

    def native_failure(self) -> None:
        base = Path("/work/native-failure")
        source = base / "locked/source.yaml"
        home, root = base / "home", base / "nido"
        home.mkdir(parents=True)
        write(source, TARGET_CONDARC, 0o600)
        env = dict(self.base, HOME=str(home), CONDARC=str(source))
        cli = [self.args.binary, "--root", str(root)]
        self.run([*cli, "init", "--yes"], env)
        source.parent.chmod(0o500)
        before = source.read_bytes()
        env_before = (root / "env.sh").read_bytes()
        try:
            self.run([*cli, "conda", "mirror", "conda", "official", "--yes"], env, fail=True)
            equal(source.read_bytes(), before, "failed native write damaged source")
            cfg = tomllib.loads((root / "config.toml").read_text())["conda"]
            assert not cfg.get("conda_mirror"), "failed selection committed intent"
            # This failure happens after valid capture/checkpoint, before the
            # main selection commit. Its original default must remain durable.
            assert cfg["defaults"].get("conda_captured"), "valid original default was lost"
            assert "mirrors.tuna.tsinghua.edu.cn" in cfg["defaults"]["conda_yaml"]
            assert not (home / ".condarc").exists(), "earlier user-file write was not compensated"
            equal((root / "env.sh").read_bytes(), env_before, "failed selection changed environment")
            assert not list(source.parent.glob(".nido-native-*")), "failed write leaked temporary files"
        finally:
            source.parent.chmod(0o700)
        self.run([*cli, "conda", "mirror", "conda", "official", "--yes"], env)

    def environment_protection(self) -> None:
        home = Path("/work/environment/home")
        root = home.parent / "nido"
        write(home / ".condarc", TARGET_CONDARC)
        certificate = home.parent / "certificate.pem"
        write(certificate, "ISOLATED CERTIFICATE CONTENT\n", 0o600)
        protected = {"CONDA_SSL_VERIFY": str(certificate),
                     "REQUESTS_CA_BUNDLE": str(certificate),
                     "HTTPS_PROXY": "http://127.0.0.1:9",
                     "HTTP_PROXY": "http://127.0.0.1:9"}
        env = dict(self.base, HOME=str(home), CONDA_CHANNEL_ALIAS="https://original.example.invalid/cloud", **protected)
        cli = [self.args.binary, "--root", str(root)]

        def show(keys, current):
            return json.loads(self.run([CONDA, "config", "--show", *keys, "--json"], current))

        def cold():
            output = self.run(["/bin/bash", "--noprofile", "--norc", "-c",
                               '. "$1"; env -0', "p0-environment", str(root / "env.sh")], env)
            return dict(item.split("=", 1) for item in output.split("\0") if item)

        mirrors_before = show(MIRROR_KEYS, env)
        tls_before = show(("ssl_verify", "proxy_servers"), env)
        self.run([*cli, "init", "--yes"], env)
        self.run([*cli, "conda", "setup", "conda", "--yes"], env)
        defaults = tomllib.loads((root / "config.toml").read_text())["conda"]["defaults"]
        equal(defaults["conda_variables"]["CONDA_CHANNEL_ALIAS"], env["CONDA_CHANNEL_ALIAS"], "original channel variable not persisted")
        for selection in ("tuna", "default"):
            self.run([*cli, "conda", "mirror", "conda", selection, "--yes"], env)
            active = cold()
            for key, value in protected.items():
                equal(active.get(key), value, f"mirror changed certificate/proxy variable {key}")
            equal(show(("ssl_verify", "proxy_servers"), active), tls_before, "mirror changed effective TLS/proxy")
            if selection == "default":
                equal(active.get("CONDA_CHANNEL_ALIAS"), env["CONDA_CHANNEL_ALIAS"], "default lost channel override")
                equal(show(MIRROR_KEYS, active), mirrors_before, "default lost effective overridden mirrors")
            else:
                assert "CONDA_CHANNEL_ALIAS" not in active, "preset did not release mirror override"
            self.run([*cli, "conda", "validate", "--json"], active)
        equal(certificate.read_text(), "ISOLATED CERTIFICATE CONTENT\n", "certificate content changed")

    def upstream_suite(self, filename: str, *args: str) -> None:
        # The multi-scenario helper performs many independently bounded local
        # queries; tracing under emulation is slower than a single CLI command.
        self.run([PYTHON, "-B", str(Path("/inputs") / filename), self.args.binary, *args], timeout=600)

    def execute(self) -> int:
        # Hash actual runtime content and metadata in both modes; normal mode
        # permits upstream's a+ probe but must not mutate the installation.
        runtime_before = digest_tree(PREFIX)
        self.group("exact-runtime-and-kernel-boundary", self.identity)
        self.group("target-implicit-config-default-and-native-paths", self.target)
        self.group("dual-user-pip-precedence", self.pip_precedence)
        self.group("native-write-failure-and-retry", self.native_failure)
        self.group("mirror-and-tls-proxy-environment-protection", self.environment_protection)
        self.group("custom-condarc-and-prefix-protection", lambda: self.upstream_suite("conda-source-upstream-isolation.py", CONDA))
        self.group("explicit-pip-config-protection", lambda: self.upstream_suite("pip-upstream-isolation.py"))
        self.group("runtime-content-and-metadata-preserved", lambda: equal(
            digest_tree(PREFIX), runtime_before, "upstream runtime changed"))
        passed = all(item["passed"] for item in self.results)
        result = {"mode": self.args.mode, "passed": passed, "groups": self.results,
                  "nido_version": self.nido_version,
                  "binary_sha256": hashlib.sha256(Path(self.args.binary).read_bytes()).hexdigest(),
                  "external_network": "disabled by Docker --network none",
                  "runtime_manifest_sha256": hashlib.sha256(json.dumps(runtime_before, sort_keys=True).encode()).hexdigest(),
                  "syscall_trace": os.environ.get("NIDO_P0_TRACE_STATUS", "unavailable"),
                  "limitations": ["amd64 execution may be emulated; inspect outer receipt",
                                  "no real GPFS, platform hook or existing environment activation"]}
        (self.output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        return 0 if passed else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--mode", choices=("normal", "strict"), required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--expected-version", default="")
    arguments = parser.parse_args()
    sys.exit(Acceptance(arguments).execute())
