# Trust Wallet Core

`wallet-core/` is a shallow clone of https://github.com/trustwallet/wallet-core.

The Go integration is wired through `blockchain/walletcore`:

- default build: pure-Go fallback so local tests and development do not require native WalletCore binaries;
- `go build -tags trustwalletcore`: CGO adapter links against `third_party/trustwallet/wallet-core/build`.

Before building with `-tags trustwalletcore`, build Wallet Core from `third_party/trustwallet/wallet-core` with its official `bootstrap.sh` flow so `libTrustWalletCore` and related native libraries exist.
