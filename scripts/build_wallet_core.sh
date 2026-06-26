#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
WALLET_CORE_DIR="$ROOT_DIR/third_party/trustwallet/wallet-core"

if [ ! -d "$WALLET_CORE_DIR" ]; then
  echo "wallet-core source not found: $WALLET_CORE_DIR" >&2
  exit 1
fi

cd "$WALLET_CORE_DIR"

if [ "$(uname -s)" = "Darwin" ]; then
  tools/install-sys-dependencies-mac
else
  tools/install-sys-dependencies-linux
fi

tools/install-dependencies
tools/install-rust-dependencies dev
tools/generate-files native

cmake -H. -Bbuild -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++
cmake --build build --target TrustWalletCore -j "$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
