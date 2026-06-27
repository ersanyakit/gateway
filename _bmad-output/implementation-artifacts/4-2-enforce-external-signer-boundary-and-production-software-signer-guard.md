---
story_id: "4.2"
story_key: "4-2-enforce-external-signer-boundary-and-production-software-signer-guard"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 74d9e4007297893d6c36dbc6f9f286914e157bf8
---

# Story 4.2: Enforce External Signer Boundary and Production Software-Signer Guard

Status: review

## Story

Bir custody operatoru olarak,
outbound signing islemlerinin signer boundary uzerinden gecmesini ve production ortaminda software signer'in bloklanmasini istiyorum,
boylece private key ve mnemonic approved custody layer disina cikmaz.

## Acceptance Criteria

1. Given an outbound transaction intent is ready for signing, when the application requests a signature, then it sends key reference, chain, derivation/account context, transaction intent, amount, destination, and policy metadata to the signer boundary, and it does not expose private keys, mnemonics, or raw seed material to application callers.
2. Given the environment is production, when `SIGNER_MODE=software` or equivalent development signer is selected, then signing hard-fails before transaction construction or broadcast, and the failure is audit logged as a production custody gate.
3. Given KMS, HSM, MPC, Vault, or external custody signing is not yet configured, when production outbound signing is requested, then the system returns an explicit integration-required failure and does not fall back to process-memory mnemonic signing.
4. Given a signing request is completed or rejected, when audit logs are written, then logs include signer mode, key reference, actor or job id, policy decision, request correlation id, and outcome, and logs exclude secret material and raw signatures unless explicitly safe for audit storage.
5. Given signer boundary enforcement is implemented, when automated tests run, then they cover development software signing allowance, production hard-fail, missing external signer, audit logging, and secret redaction.

## Tasks / Subtasks

- [x] Task 1: Ortak signer policy boundary olustur (AC: 1, 2, 3, 4, 5)
  - [x] Yeni external dependency eklemeden kucuk bir `services/signer` paketi veya mevcut pattern ile uyumlu signer-policy boundary ekle.
  - [x] `SIGNER_MODE` degerini normalize et; bos mod development icin `software` kabul edilsin.
  - [x] `APP_ENV=production` iken `software` mode her durumda hard-fail etsin; `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=true` artik signing allowance olmasin.
  - [x] `kms`, `hsm`, `mpc`, `vault` ve esdeger external custody modlari gercek adapter yoksa explicit integration-required hata dondursun.
  - [x] Policy sonucu sanitized audit/event metadata uretebilsin: signer mode, key reference, chain, intent, actor/job id, correlation id, decision ve outcome.

- [x] Task 2: Derived wallet'lara secret olmayan signer metadata ekle (AC: 1, 4)
  - [x] `blockchain.WalletDetails` icine secret olmayan signer context ekle: key reference, derivation path/account context ve signer mode snapshot gibi alanlar.
  - [x] `BaseChain.GetDerivedWallet` bu context'i doldursun; `CreateHDWallet` path'leri key reference ve derivation/account context tasiyabilsin.
  - [x] `PrivateKey` ve `MnemonicPhrase` mevcut development software signing icin kalabilir, ancak policy/audit payload'larina, API response'lara veya log'lara girmemeli.
  - [x] `GetMnemonic` icindeki production software guard yeni policy logic'ine delegasyon yapmali; readiness ve chain signing ayni kural setini kullanmali.

- [x] Task 3: Tum local chain signing path'lerini tx build/sign/broadcast oncesi gate et (AC: 1, 2, 3)
  - [x] EVM native, ERC-20, sweep, sweep-to, ERC-20 sweep ve gas/prefund signing path'leri private-key signing veya Trust Wallet Core input olusturmadan once signer policy cagirir.
  - [x] Bitcoin send ve sweep path'leri private key decode, Trust Wallet Core signing veya manual Taproot signing oncesinde signer policy cagirir.
  - [x] Solana lamport, SPL, sweep, SPL sweep ve gas prefund path'leri transaction build/sign/broadcast oncesinde signer policy cagirir.
  - [x] TRON TRX, TRC-20, sweep, TRC-20 sweep ve gas prefund path'leri raw tx signing/broadcast oncesinde signer policy cagirir.
  - [x] Story 4.1 ledger hold/reservation davranisini koru; unreserved direct sweep/withdrawal path'lerini geri acma.

- [x] Task 4: Readiness, metrics ve dokumanlari policy boundary'ye bagla (AC: 2, 3, 4)
  - [x] `api/handlers/v1_readiness.go` icindeki production signer readiness env parsing'i yeni signer policy'yi reuse etsin.
  - [x] `api/handlers/metrics.go` icindeki `gateway_production_signer_ready` gauge readiness ile ayni policy sonucunu kullansin.
  - [x] Hata mesajlari operator icin actionable olsun, ama `MNEMONIC_PHRASE`, private key, raw signature, full payload veya credential degeri icermesin.
  - [x] `README.md` ve `SECURITY.md` icinde production software override'in signing allowance oldugunu ima eden metinleri guncelle; override readiness blocker olarak kalabilir ama production signing'i geciremez.

- [x] Task 5: Test ve validation kanitlarini ekle (AC: 1, 2, 3, 4, 5)
  - [x] Signer policy unit testleri: development software allowance, production software hard-fail, legacy override hard-fail, unimplemented external mode failure, unsupported mode, audit field seti ve secret redaction.
  - [x] En az bir EVM local transfer path'i icin production guard'in RPC/private-key signing oncesi calistigini kanitlayan chain-level test ekle.
  - [x] Bitcoin/Solana/TRON path'leri icin dogrudan policy helper unit testleri veya representative signing-guard tests ekle; her chain ailesinde raw private key signing'e gitmeden fail ettigini kanitla.
  - [x] Readiness/metrics testlerini policy output degisikligine gore guncelle.
  - [x] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/signer ./blockchain ./blockchain/chains ./api/handlers`.
  - [x] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.

- [x] Task 6: Story record ve completion evidence'i tamamla (AC: 1, 2, 3, 4, 5)
  - [x] Dev Agent Record, Debug Log References, Completion Notes, File List, Change Log ve status alanlarini guncelle.
  - [x] Tum task'ler ve validation gecmeden `sprint-status.yaml` icindeki story status'unu `review` yapma.

## Dev Notes

### Current Implementation Snapshot

- `services/signer` henuz yok; bu story shared signer policy boundary'yi ilk kez eklemeli.
- `blockchain.WalletDetails` su anda sadece `Address`, `PrivateKey`, `MnemonicPhrase` tasiyor. Key reference veya derivation/account context yok.
- `blockchain.BaseChain.GetMnemonic` `SIGNER_MODE` okuyor, production `software` mode'u sadece `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=true` degilse blokluyor, `kms/hsm/mpc` icin integration-required hata donduruyor. Story 4.2 bunu yeterli kabul etmemeli; chain transfer kodu zaten populated `WalletDetails.PrivateKey` ile dogrudan signing yapabiliyor.
- `BaseChain.GetDerivedWallet` derived private key ve mnemonic'i `WalletDetails` icine koyuyor. Bu development icin kalabilir, ancak production policy boundary app code'un private key/mnemonic ile signing'e devam etmesini engellemeli.
- EVM signing `blockchain/chains/evm_transfer.go` icinde `evmPrivateKeyAndAddress`, `evmSignNativeWithTrustWallet` ve `evmSignERC20WithTrustWallet` uzerinden private key'i Trust Wallet Core signing input'una koyuyor.
- Bitcoin signing `blockchain/chains/bitcoin_transfer.go` icinde `sendTo`, `SweepTo`, `signBitcoinWithTrustWallet` ve manual Taproot fallback ile private key kullanarak raw tx imzaliyor.
- Solana signing `blockchain/chains/solana.go` ve `blockchain/chains/solana_transfer.go` icinde `solanaPrivateKeyAndAddress` ve `tx.Sign` callback'leri ile private key kullaniyor.
- TRON signing `blockchain/chains/tron_transfer.go` icinde `tronPrivateKey` ve `tronSignAndBroadcast` ile raw tx signing/broadcast yapiyor.
- V1 readiness ve operational metrics signer readiness'i kendi env parsing'iyle hesapliyor; bu duplication yeni signer policy'ye tasinmali.

### Architecture And Product Guardrails

- AD-6: application services imza isterken key reference, chain, derivation/account context, transaction intent ve policy metadata gonderir; mnemonic/private key app code'a, app DB'ye veya log'a cikmaz. `SIGNER_MODE=software` sadece development'tir.
- AD-7: outbound tx ledger hold/reservation ve chain-specific ownership/resource gates olmadan sign edilmez. Story 4.3 nonce/UTXO/resource policy'yi sahiplenir; bu story signer boundary ve production software hard-fail'i sahiplenir.
- AD-12: real-funds production icin signer audit logs ve operational gates launch oncesi zorunludur.
- FR24/FR25: production signer KMS/HSM/MPC/Vault veya secilecek external custody signer olmalidir; signing request key reference, chain, derivation/account context, transaction intent ve policy metadata tasimalidir.
- NFR4/NFR13/NFR17: production custody private key/mnemonic'i process memory, app DB, logs, responses ve routine operator surfaces disinda tutmali; signer decisions auditable olmali.

### Implementation Boundaries

- Bu story gercek KMS/HSM/MPC/Vault adapter implement etmez. External mode'lar provider secilip entegre edilene kadar explicit integration-required fail etmelidir.
- Yeni broker, fiziksel service split, yeni chain SDK veya SPA ekleme.
- Public webhook HMAC semantics, V1 request auth semantics ve V1 error envelope shape degismemeli.
- `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION` dokumanda legacy/risk flag olarak kalabilir, ancak production signing allowance olmamali.
- Secret redaction zorunlu: `MNEMONIC_PHRASE`, private key, raw signature, seed material, full signed tx payload ve credential degerleri audit/log/error icinde yer almamali.
- Chain-resource reservation Story 4.3 kapsamidir; bu story nonce/UTXO/resource manager'i implement etmemeli, fakat signer guard'in tx construction/broadcast oncesi calistigini garanti etmeli.

### Previous Story Intelligence

- Story 4.1 outbound withdrawal, refund, payout ve sweep path'lerinde ledger reservation'i zorunlu hale getirdi ve direct unreserved paths'i kapatti. Bu story o gate'leri gevsetmemeli.
- Story 4.1 review sonrasi sweep hold schema index verification, sweep release mismatch validation ve refund operator audit gaps auto-fix edildi. Bu story signer audit coverage icin ayni kanit disiplinini kullanmali.
- Story 3.6 scoped reconciliation ve redacted evidence pattern'lerini kurdu. Signer/broadcast uncertainty sonradan yakalanirsa blind retry yerine scoped reconciliation job pattern'i kullanilmali; bu story icin minimum gerekli davranis policy rejection ve audit evidence'dir.

### Likely Files To Touch

- `services/signer/*` (NEW)
- `blockchain/basechain.go`
- `blockchain/basechain_test.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/evm_transfer_test.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/bitcoin_transfer_test.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`
- `blockchain/chains/solana_tron_balance_test.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/tron_transfer_test.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/v1_readiness_test.go`
- `api/handlers/metrics.go`
- `api/handlers/metrics_test.go`
- `README.md`
- `SECURITY.md`
- `_bmad-output/implementation-artifacts/4-2-enforce-external-signer-boundary-and-production-software-signer-guard.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Project Structure Notes

- Repo Go modular monolith olarak kalir; module path `core`.
- Shared policy icin small package kullan; chain transfer fonksiyonlarini genis refactor etme.
- Mevcut GORM/repository/money boundary'lerini atlama; bu story schema degisikligi gerektirmemeli.
- Worktree'de story disi UI/admin/listener degisiklikleri var; 4.2 implementasyonu bunlari revert etmemeli veya sahiplenmemeli.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.2.
- PRD FR24/FR25 and NFR4/NFR13/NFR17: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-6/AD-7/AD-12: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- UX operational visibility and audit guidance: `_bmad-output/planning-artifacts/ux.md`, `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`.
- Readiness context: `_bmad-output/planning-artifacts/implementation-readiness-report-2026-06-27.md`.
- Audit context: `docs/payment-gateway-wallet-provider-audit.md`, `docs/product-readiness-audit.md`, `SECURITY.md`.
- Project context: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/4-1-reserve-ledger-holds-before-outbound-money-movement.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created/refreshed from Epic 4.2 with PRD FR24/FR25, architecture AD-6/AD-7/AD-12, current `BaseChain.GetMnemonic` guard analysis, readiness/metrics duplication, and chain-level private-key signing bypass analysis.
- 2026-06-27: Story moved to in-progress at baseline commit `74d9e4007297893d6c36dbc6f9f286914e157bf8`; RED tests added for signer policy, wallet metadata, production software hard-fail, readiness external modes, and chain guard ordering.
- 2026-06-27: Implemented `services/signer` policy boundary with normalized modes, explicit production software hard-fail, external integration-required errors, key reference generation, and sanitized audit output.
- 2026-06-27: Added signer metadata to `WalletDetails`, routed HD wallet mnemonic access through `GetMnemonicForPath`, and gated EVM, Bitcoin, Solana, and TRON signing/prefund/sweep paths before tx build/sign/broadcast.
- 2026-06-27: Delegated V1 signer readiness and metrics signer gauge to the shared signer policy; updated production signing docs and product readiness audit.
- 2026-06-27: Removed a duplicate `sameOptionalToken` helper from the dirty ledger worktree state to restore repository compilation; no behavior change intended.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/signer ./blockchain ./blockchain/chains ./api/handlers`.
- 2026-06-27: Validation passed after metrics/signer test additions: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/signer ./api/handlers`.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...` (outside sandbox for local listener binding).
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
- 2026-06-27: Validation passed: `git diff --check && git diff --cached --check`.
- 2026-06-27: QA automation added signer metrics and unsupported-mode audit coverage; isolated validation passed for `./services/signer`, targeted `./api/handlers`, and targeted `./blockchain/chains`.

### Completion Notes List

- Shared signer policy boundary now treats `SIGNER_MODE=software` as development-only and hard-fails production signing even when legacy override flags are set.
- `kms`, `hsm`, `mpc`, `vault`, and equivalent external custody modes now fail explicitly as integration-required until a real provider adapter is active.
- HD wallet derivation now carries non-secret key reference, derivation path, and signer mode context; `CreateHDWallet` paths request mnemonic access through the same signer policy.
- EVM, Bitcoin, Solana, and TRON local signing paths now call signer policy before private-key signing, Trust Wallet Core signing input creation, transaction construction, raw signing, or broadcast.
- V1 readiness and `gateway_production_signer_ready` now share the signer policy result, and docs no longer imply a production software signing allowance.
- Signer audit output includes mode, key reference, chain, intent, destination, decision, outcome, and correlation fields while filtering secret-like metadata values.
- QA automation added metrics exposure coverage for the production software signer gate and unsupported signer mode audit/readiness tests.
- A duplicate ledger helper in the dirty worktree was removed so full regression can compile.
- Existing branch follow-ups retained and validated: 4.1 sweep/ledger review patches, tx-rescan deposit processing, TRON native log index normalization, and admin live-balance UI support.
- Full `go test ./...` passed outside the sandbox; local sandbox execution can still block tests that need localhost listener binding.

### File List

- `SECURITY.md`
- `_bmad-output/implementation-artifacts/4-1-reserve-ledger-holds-before-outbound-money-movement.md`
- `_bmad-output/implementation-artifacts/4-2-enforce-external-signer-boundary-and-production-software-signer-guard.md`
- `_bmad-output/implementation-artifacts/deferred-work.md`
- `_bmad-output/implementation-artifacts/tests/test-summary.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `api/handlers/metrics_test.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/v1_readiness_test.go`
- `api/routes/routes.go`
- `blockchain/basechain.go`
- `blockchain/basechain_test.go`
- `blockchain/chains/avalanche.go`
- `blockchain/chains/binance.go`
- `blockchain/chains/bitcoin.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/chiliz.go`
- `blockchain/chains/chiliz_spicy.go`
- `blockchain/chains/ethereum.go`
- `blockchain/chains/evm_compatible.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/signer_boundary_test.go`
- `blockchain/chains/signer_policy.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`
- `blockchain/chains/tron.go`
- `blockchain/chains/tron_transfer.go`
- `docs/product-readiness-audit.md`
- `main.go`
- `main_sweep_reservation_test.go`
- `readme.md`
- `repositories/ledger_repo.go`
- `repositories/ledger_repo_test.go`
- `services/signer/policy.go`
- `services/signer/policy_test.go`
- `services/txrescan/service.go`
- `services/txrescan/service_test.go`
- `views/assets/dashboard.js`
- `views/assets/tailwind.css`
- `views/dealer/admin_dashboard.html`
- `views/dealer/dashboard.html`
- `views/dealer/partials/footer.html`
- `views/dealer/partials/header.html`
- `workers/listeners/tron/tron.go`

### Change Log

- 2026-06-27: Created ready-for-dev story with signer boundary scope, production software signer guard, current implementation snapshot, chain signing bypass risks, tests, validation plan, and status aligned to create-story workflow.
- 2026-06-27: Implemented signer policy boundary, wallet signer metadata, production software hard-fail, chain signing guards, readiness/docs updates, tests, and moved story to review.
