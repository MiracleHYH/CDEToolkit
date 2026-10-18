#!/usr/bin/env python3
"""Offline acceptance test against an explicitly supplied local Conda binary.

Run with the project-selected Python environment, for example:
  conda run -n agent python tests/conda-source-upstream-isolation.py NIDO CONDA

All homes, Conda roots, prefixes, configuration files and data are temporary.
Only local config queries are executed: no solver, package or network operation.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


MIRROR_KEYS = (
    "channels",
    "default_channels",
    "custom_channels",
    "custom_multichannels",
    "channel_alias",
    "migrated_channel_aliases",
    "migrated_custom_channels",
)
OTHER_KEYS = (
    "ssl_verify",
    "client_ssl_cert",
    "client_ssl_cert_key",
    "proxy_servers",
    "remote_connect_timeout_secs",
    "channel_priority",
    "create_default_packages",
    "envs_dirs",
    "pkgs_dirs",
)


def run(argv: list[str], env: dict[str, str]) -> str:
    result = subprocess.run(
        argv, env=env, text=True, capture_output=True, timeout=30, check=False
    )
    if result.returncode:
        raise AssertionError(
            f"command failed ({result.returncode}): {argv!r}\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    return result.stdout


def require_equal(actual: object, expected: object, message: str) -> None:
    if actual != expected:
        raise AssertionError(
            f"{message}\nexpected: {expected!r}\nactual:   {actual!r}"
        )


def write_private(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    path.chmod(0o600)


def scalar(value: object) -> str:
    # JSON scalars and collections are also valid YAML and quote fixture paths.
    return json.dumps(value, ensure_ascii=False)


class Scenario:
    def __init__(
        self, directory: Path, nido: str, conda: str, standard_source: bool
    ) -> None:
        self.directory = directory
        self.nido_binary = nido
        self.conda_binary = conda
        self.home = directory / "home"
        self.root = directory / "software path" / "nido"
        self.prefix = directory / "active prefix"
        self.alternate = directory / "other prefix"
        self.source = (
            self.home / ".condarc"
            if standard_source
            else directory / "private source" / "profile.yaml"
        )
        self.data = directory / "data"
        self.certificate = directory / "certificate.pem"
        self.key = directory / "client.key"
        self.prefix_certificate = directory / "prefix-certificate.pem"
        self.alternate_certificate = directory / "alternate-certificate.pem"
        self.standard_source = standard_source
        for path in (
            self.home,
            directory / "tmp",
            directory / "conda root",
            directory / "xdg config",
            directory / "xdg data",
            directory / "xdg cache",
            self.data / "envs",
            self.data / "pkgs",
        ):
            path.mkdir(parents=True)
        for prefix in (self.prefix, self.alternate):
            write_private(prefix / "conda-meta" / "history", "# isolated fixture\n")
        for certificate in (
            self.certificate,
            self.key,
            self.prefix_certificate,
            self.alternate_certificate,
        ):
            write_private(certificate, "ISOLATED CERTIFICATE FIXTURE\n")
        write_private(self.data / "envs" / "keep.txt", "ENV DATA SENTINEL\n")
        write_private(self.data / "pkgs" / "keep.txt", "CACHE DATA SENTINEL\n")
        self.sentinels = {
            path: path.read_bytes() for path in self.data.rglob("*") if path.is_file()
        }
        self.env = {
            "HOME": str(self.home),
            "PATH": f"{Path(conda).parent}:/usr/bin:/bin",
            "TMPDIR": str(directory / "tmp"),
            "XDG_CONFIG_HOME": str(directory / "xdg config"),
            "XDG_CONFIG_DIRS": str(directory / "xdg config dirs"),
            "XDG_DATA_HOME": str(directory / "xdg data"),
            "XDG_DATA_DIRS": str(directory / "xdg data dirs"),
            "XDG_CACHE_HOME": str(directory / "xdg cache"),
            "CONDA_ROOT": str(directory / "conda root"),
            "CONDA_PREFIX": str(self.prefix),
            "CONDA_EXE": conda,
            "CONDARC": str(self.source),
            "CONDA_OFFLINE": "true",
            "CONDA_NO_PLUGINS": "true",
            "CONDA_REPORT_ERRORS": "false",
            "PYTHONDONTWRITEBYTECODE": "1",
            "PIP_DISABLE_PIP_VERSION_CHECK": "1",
            "PIP_CONFIG_FILE": os.devnull,
        }
        self.base_env = self.env.copy()
        self.create_source()
        self.create_prefix(self.prefix, self.prefix_certificate, "active", 47)
        self.create_prefix(self.alternate, self.alternate_certificate, "alternate", 57)
        self.prefix_configs = {
            prefix / ".condarc": (prefix / ".condarc").read_bytes()
            for prefix in (self.prefix, self.alternate)
        }

    def create_source(self) -> None:
        # A $CONDARC reference proves the original environment value survives;
        # it is only queried and never used to open a TLS connection.
        source = f"""# SOURCE HEADER: preserve certificate, proxy and unknown configuration.
channels: #!final
  - https://original.example.invalid/pkgs/main
  - conda-forge
default_channels: #!final
  - https://original.example.invalid/pkgs/main
  - https://original.example.invalid/pkgs/r
  - https://original.example.invalid/pkgs/msys2
custom_channels: #!final
  conda-forge: https://original.example.invalid/cloud
  bioconda: https://original.example.invalid/cloud
  private: https://private.example.invalid/channels
custom_multichannels: #!final
  defaults:
    - https://original.example.invalid/pkgs/main
    - https://original.example.invalid/pkgs/r
    - https://original.example.invalid/pkgs/msys2
channel_alias: https://original.example.invalid/cloud #!final
migrated_channel_aliases: [] #!final
migrated_custom_channels: {{}} #!final
ssl_verify: {scalar(str(self.certificate))}
client_ssl_cert: "$CONDARC"
client_ssl_cert_key: {scalar(str(self.key))}
proxy_servers:
  https: http://source-proxy.example.invalid:8080
remote_connect_timeout_secs: 37
channel_priority: strict
create_default_packages:
  - source-fixture-package
envs_dirs: {scalar([str(self.data / 'envs')])}
pkgs_dirs: {scalar([str(self.data / 'pkgs')])}
nido_source_sentinel:
  message: keep private unknown settings
  values: [one, two]
# SOURCE FOOTER: keep this comment.
"""
        write_private(self.source, source)
        if not self.standard_source:
            write_private(self.home / ".condarc", "# ordinary user configuration\n")

    def create_prefix(
        self, prefix: Path, certificate: Path, label: str, timeout: int
    ) -> None:
        write_private(
            prefix / ".condarc",
            f"""# {label} environment configuration must keep its priority.
ssl_verify: {scalar(str(certificate))}
proxy_servers:
  https: http://{label}-proxy.example.invalid:8080
remote_connect_timeout_secs: {timeout}
create_default_packages:
  - {label}-fixture-package
""",
        )

    def nido(self, *arguments: str) -> str:
        return run(
            [self.nido_binary, "--root", str(self.root), *arguments], self.env
        )

    def reload(self) -> None:
        output = subprocess.run(
            [
                "/bin/sh",
                "-c",
                '. "$1"; env -0',
                "isolated-source-test",
                str(self.root / "env.sh"),
            ],
            env=self.env,
            capture_output=True,
            timeout=10,
            check=True,
        ).stdout
        self.env = {
            key.decode(): value.decode()
            for item in output.split(b"\0")
            if item
            for key, value in [item.split(b"=", 1)]
        }
        require_equal(
            self.env.get("CONDARC"),
            str(self.source),
            "reloading Nido must preserve the exact original CONDARC value",
        )

    def show(
        self,
        keys: tuple[str, ...],
        *,
        env: dict[str, str] | None = None,
        alternate: bool = False,
    ) -> dict[str, object]:
        arguments = [self.conda_binary, "config"]
        if alternate:
            arguments += ["--prefix", str(self.alternate)]
        arguments += ["--show", *keys, "--json"]
        return json.loads(run(arguments, self.env if env is None else env))

    def source_values(self) -> dict[str, object]:
        sources = json.loads(
            run(
                [self.conda_binary, "config", "--show-sources", "--json"],
                self.base_env,
            )
        )
        source = sources[str(self.source)]
        return {key: value for key, value in source.items() if key not in MIRROR_KEYS}

    def check_other_settings(
        self,
        expected: dict[str, object],
        alternate_expected: dict[str, object],
        source_expected: dict[str, object],
    ) -> None:
        require_equal(self.show(OTHER_KEYS), expected, "effective settings changed")
        require_equal(
            self.show(OTHER_KEYS, alternate=True),
            alternate_expected,
            "--prefix changed the original non-mirror search precedence",
        )
        require_equal(
            self.source_values(), source_expected, "unrelated source keys changed"
        )
        require_equal(
            self.show(("client_ssl_cert",))["client_ssl_cert"],
            str(self.source),
            "$CONDARC expansion in a certificate setting changed",
        )
        for path, content in self.sentinels.items():
            require_equal(path.read_bytes(), content, "environment/cache data changed")
        for path, content in self.prefix_configs.items():
            require_equal(path.read_bytes(), content, "unselected prefix configuration changed")
        require_equal(
            self.source.stat().st_mode & 0o777,
            0o600,
            "source configuration lost its private permissions",
        )
        for comment in ("# SOURCE HEADER:", "# SOURCE FOOTER:"):
            if comment not in self.source.read_text(encoding="utf-8"):
                raise AssertionError("an unrelated source comment was lost")

    def execute(self) -> None:
        before = self.show(OTHER_KEYS)
        alternate_before = self.show(OTHER_KEYS, alternate=True)
        mirrors_before = self.show(MIRROR_KEYS)
        source_before = self.source_values()
        if self.standard_source:
            require_equal(
                before["ssl_verify"],
                str(self.prefix_certificate),
                "fixture must exercise environment precedence over standard CONDARC",
            )
            require_equal(
                alternate_before["ssl_verify"],
                str(self.alternate_certificate),
                "fixture must exercise --prefix certificate precedence",
            )
        else:
            require_equal(
                before["ssl_verify"],
                str(self.certificate),
                "nonstandard explicit CONDARC must retain its original last position",
            )
        self.nido("init", "--yes")
        self.nido("conda", "setup", "conda", "--yes")
        for mirror in ("official", "tuna"):
            source_bytes = self.source.read_bytes()
            config_bytes = (self.root / "config.toml").read_bytes()
            self.nido("conda", "mirror", "conda", mirror, "--dry-run")
            require_equal(self.source.read_bytes(), source_bytes, "dry-run edited source")
            require_equal(
                (self.root / "config.toml").read_bytes(),
                config_bytes,
                "dry-run saved a selection",
            )
            self.nido("conda", "mirror", "conda", mirror, "--yes")
            self.reload()
            self.check_other_settings(before, alternate_before, source_before)
            self.nido("conda", "validate", "--json")

        # Replace the file atomically, as editors commonly do. Active settings
        # must follow the same path and immediately expose the replacement.
        replacement = self.source.with_name("editor-replacement.yaml")
        write_private(
            replacement,
            self.source.read_text(encoding="utf-8").replace(
                "source-proxy.example.invalid", "edited-proxy.example.invalid"
            ).replace(
                "remote_connect_timeout_secs: 37", "remote_connect_timeout_secs: 39"
            ).replace(
                "keep private unknown settings", "keep edited unknown settings"
            ),
        )
        os.replace(replacement, self.source)
        updated = self.show(OTHER_KEYS, env=self.base_env)
        alternate_updated = self.show(OTHER_KEYS, env=self.base_env, alternate=True)
        source_updated = self.source_values()
        self.check_other_settings(updated, alternate_updated, source_updated)
        if not self.standard_source:
            require_equal(
                updated["remote_connect_timeout_secs"],
                39,
                "atomic replacement did not become effective immediately",
            )

        # Releasing a preset must retain its inplace native values, rather than
        # using the original default as an implicit rollback.
        source_bytes = self.source.read_bytes()
        tuna_mirrors = self.show(MIRROR_KEYS)
        self.nido("conda", "mirror", "conda", "off", "--yes")
        self.reload()
        require_equal(self.source.read_bytes(), source_bytes, "off rolled back native mirror keys")
        require_equal(self.show(MIRROR_KEYS), tuna_mirrors, "off changed the selected native mirror")
        self.check_other_settings(updated, alternate_updated, source_updated)

        self.nido("conda", "mirror", "conda", "default", "--yes")
        self.reload()
        self.check_other_settings(updated, alternate_updated, source_updated)
        require_equal(
            self.show(MIRROR_KEYS), mirrors_before, "default lost original mirror semantics"
        )
        self.nido("conda", "validate", "--json")
        source_bytes = self.source.read_bytes()
        user_bytes = (self.home / ".condarc").read_bytes()
        self.nido("conda", "mirror", "conda", "off", "--yes")
        self.reload()
        self.check_other_settings(updated, alternate_updated, source_updated)
        require_equal(self.source.read_bytes(), source_bytes, "off rewrote native settings")
        require_equal(
            (self.home / ".condarc").read_bytes(), user_bytes, "off rewrote user settings"
        )
        self.nido("conda", "reset", "--yes")
        self.reload()
        self.check_other_settings(updated, alternate_updated, source_updated)
        require_equal(self.source.read_bytes(), source_bytes, "reset rewrote native settings")
        label = "standard ~/.condarc" if self.standard_source else "nonstandard CONDARC"
        print(f"PASS {label}: mirror/default/off, TLS/proxy, live edits and --prefix")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("nido_binary", help="absolute path to the built Nido binary")
    parser.add_argument("conda_binary", help="absolute path to an existing Conda binary")
    arguments = parser.parse_args()
    for name in ("nido_binary", "conda_binary"):
        path = Path(getattr(arguments, name))
        if not path.is_absolute() or not path.is_file() or not os.access(path, os.X_OK):
            parser.error(f"{name} must be an absolute executable file")
    with tempfile.TemporaryDirectory(prefix="nido-condarc-upstream-") as temporary:
        directory = Path(temporary).resolve()
        for standard_source in (False, True):
            scenario_root = directory / ("standard" if standard_source else "custom")
            Scenario(
                scenario_root,
                arguments.nido_binary,
                arguments.conda_binary,
                standard_source,
            ).execute()
    print("PASS real Conda queries were offline and confined to temporary configuration")


if __name__ == "__main__":
    main()
