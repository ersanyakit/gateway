#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
WALLET_CORE_DIR="$ROOT_DIR/third_party/trustwallet/wallet-core"

if [ ! -d "$WALLET_CORE_DIR" ]; then
  echo "wallet-core source not found: $WALLET_CORE_DIR" >&2
  exit 1
fi

cd "$WALLET_CORE_DIR"
./bootstrap.sh
