#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
python3 scripts/verify-repository.py
sh server/scripts/verify.sh
