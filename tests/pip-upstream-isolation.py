"""Offline acceptance against an explicitly supplied Nido and this Python's pip.

Run with the project-selected Python environment:
  conda run -n agent python tests/pip-upstream-isolation.py /absolute/path/to/nido

All writable state and configuration are temporary; no package or network
operation is performed. The supplied executable must be independently trusted.
"""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("nido_binary", help="absolute path to a trusted Nido executable")
arguments = parser.parse_args()
supplied = Path(arguments.nido_binary)
if not supplied.is_absolute() or not supplied.is_file() or not os.access(supplied, os.X_OK):
    parser.error("nido_binary must be an absolute executable file")
nido = str(supplied.resolve(strict=True))
python = str(Path(sys.executable).resolve(strict=True))


def run(args, env):
    result = subprocess.run(args, env=env, text=True, capture_output=True, timeout=40)
    if result.returncode:
        raise AssertionError((args, result.returncode, result.stdout, result.stderr))
    return result.stdout.strip()


with tempfile.TemporaryDirectory(prefix="nido-real-pip-") as scratch:
    base = Path(scratch).resolve()
    home = base / "home"
    home.mkdir()
    root = home / ".nido"
    pip_file = home / "custom-pip.conf"
    original = "https://original.invalid/simple"
    other = "cert = /fixture/certificate.pem\nproxy = http://127.0.0.1:9\ntimeout = 13\n"
    pip_file.write_text("# preserve original settings\n[global]\nindex-url = " + original + "\n" + other)
    pip_file.chmod(0o600)
    env = {
        "HOME": str(home), "NIDO_HOME": str(root), "PATH": str(Path(python).parent) + ":/usr/bin:/bin",
        "TMPDIR": str(base), "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_CACHE_HOME": str(home / ".cache"), "XDG_DATA_HOME": str(home / ".local/share"),
        "PIP_CONFIG_FILE": str(pip_file), "PIP_DISABLE_PIP_VERSION_CHECK": "1",
        "PYTHONDONTWRITEBYTECODE": "1", "BASH_ENV": "/dev/null", "ENV": "/dev/null",
    }

    def command(*args):
        return run([nido, *args], env)

    def activated(*args):
        return run(["/bin/bash", "--noprofile", "--norc", "-c", '. "$NIDO_HOME/env.sh"; exec "$@"', "accept", *args], env)

    print(run([python, "-m", "pip", "--version"], env))
    command("--root", str(root), "init", "--yes")
    command("conda", "setup", "pip", "--yes")
    command("conda", "mirror", "pip", "tuna", "--yes")
    activated(nido, "conda", "validate", "--json")
    cache = home / "native-cache" / "pip"
    command("conda", "cache", "pip", str(cache), "--yes")
    assert run([python, "-m", "pip", "cache", "dir"], env) == str(cache)
    activated(nido, "conda", "validate", "--json")
    # Cold process: no previous generated environment is inherited here.
    command("conda", "reset", "--yes")
    command("conda", "mirror", "pip", "default", "--yes")
    activated(nido, "conda", "validate", "--json")
    selected = activated(python, "-c", 'import os; print(os.environ["PIP_INDEX_URL"])')
    assert selected == original, selected
    command("conda", "setup", "pip", "--yes")
    command("conda", "reset", "--yes")
    assert run([python, "-m", "pip", "cache", "dir"], env) == str(cache)
    after = pip_file.read_text()
    assert other in after and "# preserve original settings" in after
    assert "index-url = " + original in after
    assert original in (root / "config.toml").read_text()
    assert pip_file.stat().st_mode & 0o777 == 0o600
    assert not cache.exists(), "query created the cache data directory"
    print("PASS public Nido + real pip: persistent original default, cold restore, native cache, cert/proxy, permissions and reset preservation")
    print("PASS temporary HOME and explicit temporary pip config only; no network or package operation")
