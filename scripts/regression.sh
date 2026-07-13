#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
WALLET_CORE_DIR="$ROOT_DIR/third_party/trustwallet/wallet-core"
MODE="${GATEWAY_REGRESSION_MODE:-native}"
GO_TEST_FLAGS="${GO_TEST_FLAGS:--p=1 -count=1}"

dependency_failure() {
  echo "regression dependency failure: $*" >&2
  echo "This is an environment/setup failure, not a Go test failure." >&2
  exit 70
}

assert_wallet_core_source() {
  if [ ! -d "$WALLET_CORE_DIR" ]; then
    dependency_failure "Trust Wallet Core source is missing at $WALLET_CORE_DIR. Run: git submodule update --init --recursive third_party/trustwallet/wallet-core"
  fi
  if [ ! -f "$WALLET_CORE_DIR/CMakeLists.txt" ] || [ ! -f "$WALLET_CORE_DIR/samples/go/go.mod" ]; then
    dependency_failure "Trust Wallet Core submodule is incomplete at $WALLET_CORE_DIR. Run: git submodule update --init --recursive third_party/trustwallet/wallet-core"
  fi
}

cd "$ROOT_DIR"

case "$MODE" in
  native)
    assert_wallet_core_source
    if ! "$ROOT_DIR/scripts/build_wallet_core.sh"; then
      dependency_failure "Trust Wallet Core native build failed. Check clang/cmake/rust/protobuf system dependencies."
    fi
    CGO_ENABLED="${CGO_ENABLED:-1}" go test $GO_TEST_FLAGS ./...
    CGO_ENABLED="${CGO_ENABLED:-1}" go vet ./...
    ;;
  fallback)
    assert_wallet_core_source
    echo "Running walletcorefallback regression. Native signing/address derivation tests are isolated by the walletcorefallback build tag."
    go test -tags walletcorefallback $GO_TEST_FLAGS ./...
    go vet -tags walletcorefallback ./...
    ;;
  *)
    echo "unknown GATEWAY_REGRESSION_MODE=$MODE; expected native or fallback" >&2
    exit 64
    ;;
esac
