#!/usr/bin/env bash

set -euo pipefail
# Kept because the frozen Bash update preflight checks this path exists.
# Current checks have one implementation in Make/Go.
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
exec make -C "${ROOT}" check "$@"
