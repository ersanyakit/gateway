#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
WALLET_CORE_DIR="$ROOT_DIR/third_party/trustwallet/wallet-core"

if [ -z "${CARGO_HOME:-}" ]; then
  CARGO_HOME="$WALLET_CORE_DIR/build/cargo-home"
fi
mkdir -p "$CARGO_HOME/bin"
export CARGO_HOME

for rust_bin_dir in "$HOME/.cargo/bin" /opt/homebrew/opt/rustup/bin /usr/local/opt/rustup/bin; do
  if [ -d "$rust_bin_dir" ]; then
    PATH="$rust_bin_dir:$PATH"
  fi
done
PATH="$CARGO_HOME/bin:$PATH"
export PATH

if [ "$(uname -s)" = "Darwin" ] && ! command -v rustup >/dev/null 2>&1; then
  brew install rustup
  for rust_bin_dir in /opt/homebrew/opt/rustup/bin /usr/local/opt/rustup/bin; do
    if [ -d "$rust_bin_dir" ]; then
      PATH="$rust_bin_dir:$PATH"
    fi
  done
  export PATH
  rustup toolchain install stable --profile minimal --no-self-update
fi

if [ ! -d "$WALLET_CORE_DIR" ]; then
  echo "wallet-core source not found: $WALLET_CORE_DIR" >&2
  echo "Run: git submodule update --init --recursive third_party/trustwallet/wallet-core" >&2
  exit 1
fi

if [ ! -f "$WALLET_CORE_DIR/CMakeLists.txt" ] || [ ! -f "$WALLET_CORE_DIR/samples/go/go.mod" ]; then
  echo "wallet-core submodule is not initialized correctly: $WALLET_CORE_DIR" >&2
  echo "Run: git submodule update --init --recursive third_party/trustwallet/wallet-core" >&2
  exit 1
fi

cd "$WALLET_CORE_DIR"

if [ "$(uname -s)" = "Darwin" ]; then
  tools/install-sys-dependencies-mac
else
  tools/install-sys-dependencies-linux
fi

tools/install-dependencies
tools/install-rust-dependencies
tools/generate-files native

cmake -H. -Bbuild -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++
cmake --build build --target TrustWalletCore -j "$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
