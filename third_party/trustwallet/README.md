# Trust Wallet Core

`wallet-core/` is a required git submodule of https://github.com/trustwallet/wallet-core.

Initialize it after a normal clone:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

The Go integration is wired through `blockchain/walletcore`:

- default build: CGO adapter links against `third_party/trustwallet/wallet-core/build`;
- `walletcorefallback` exists only for narrow local debug and is not production-capable.

Before building the gateway, run `./scripts/build_wallet_core.sh` so `libTrustWalletCore` and related native libraries exist.
