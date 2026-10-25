#!/usr/bin/env bash
# Run in Git Bash or MSYS2 with the native Windows Go toolchain and MinGW-w64.
set -Eeuo pipefail
source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/build-common.sh"
build_main windows "$@"
