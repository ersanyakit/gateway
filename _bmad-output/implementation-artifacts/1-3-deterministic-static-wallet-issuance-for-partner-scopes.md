---
story_id: "1.3"
story_key: "1-3-deterministic-static-wallet-issuance-for-partner-scopes"
epic: "Epic 1: Partner Integration & Payment Intake Hardening"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: e564b465bf112b6802bbcf188d53cc22b3c65fe9
---

# Story 1.3: Deterministic Static Wallet Issuance for Partner Scopes

Status: done

## Story

Bir developer integrator olarak,
dealer/merchant tenant, domain, product ve user scope icin static deposit wallet'in deterministik uretilmesini istiyorum,
boylece merchant ve exchange entegrasyonlari ayni deposit wallet'i duplicate address ownership olusturmadan guvenle tekrar isteyebilir.

## Requirements Trace

- **FRs:** FR3, FR7, FR8, FR10
- **NFRs:** NFR2, NFR4, NFR7, NFR14, NFR18
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Authenticated partner veya dealer portal action, dealer/merchant tenant, domain, product ve user scope icin static wallet istediginde ve bu scope icin wallet yoksa, sistem desteklenen chain family icin yeni wallet/address set turetir, saklar ve wallet'i request yapan tenant/domain scope'una ait kaydeder.
2. Ayni dealer/merchant tenant, domain, product ve user scope tekrar wallet istediginde sistem mevcut wallet/address set'i dondurur; HD index artmaz, duplicate ownership olusmaz ve conflicting address record yaratmaz.
3. Iki concurrent request ayni wallet scope'u hedeflediginde yalnizca tek wallet/address set olusur; davranis transaction, lock veya uniqueness guarantee ile korunur ve test edilir.
4. Desteklenen chain icin address uretilirken mevcut mimariye gore Trust Wallet Core derivation provider kullanilir ve sonra deposit matching icin yeterli chain/address metadata kaydedilir.
5. Trust Wallet Core derivation unavailable ise veya production'da fallback provider kullanilacaksa sistem address dondurmeden once fail-safe calisir; fallback invalid/placeholder production wallet uretemez.
6. Partner disabled/unsupported chain/token icin wallet isterse sistem backwards-compatible v1 error envelope ile reject eder ve partial wallet scope, HD index mutation veya address ownership record commit etmez.
7. Automated testler first issuance, idempotent repeat issuance, concurrent issuance, unsupported chain rejection, Trust Wallet Core provider usage, fallback production guard, dealer/merchant portal scope ve tenant scope isolation davranislarini kapsar.

## Tasks / Subtasks

- [x] Task 1: Static wallet request contract ve scope'u netlestir (AC: 1, 2, 6, 7)
  - [x] `types.V1StaticAddressRequest` icin `user_id`, `chain_id`, `symbol` ve gerekiyorsa `token`/token identifier alanlarini normalize et; non-native asset icin token ambiguity olusmasina izin verme.
  - [x] Product scope'u deterministik hale getir: mevcut `static:<chain_id>:<SYMBOL>` davranisini koru, ama token veya explicit product scope gerekiyorsa stable ve backwards-compatible product id formatina dahil et.
  - [x] Request yapan domain'i `v1ResolveSignedDomain` ile cozmeye devam et; tenant/domain izolasyonunu merchant-global veya user-global scope'a genisletme.
  - [x] Response sozlesmesinde `wallet_id`, `user_id`, `product_id`, `chain`, `chain_id`, `symbol`, `token` gerekiyorsa, `address`, `label`, `created_at` alanlarini stabil tut.

- [x] Task 2: Wallet issuance idempotency ve concurrency invariantlarini saglamlastir (AC: 1, 2, 3, 7)
  - [x] `WalletRepo.Create` sahibi olan repository boundary'sini kullan; handler icinden direkt wallet tablosu mutate etme.
  - [x] Scope uniqueness icin mevcut `ux_wallet_owner` (`merchant_id`, `domain_id`, `product_id`, `user_id`) constraint'ini ve `pg_advisory_xact_lock` HD index allocation guard'ini koru.
  - [x] Same-scope repeat request'in existing wallet'i dondurdugunu ve `hd_address_id`/wallet count artmadigini test et.
  - [x] Concurrent same-scope request icin tek wallet olustugunu test et; unique violation race'i gorulurse duplicate yaratmadan existing scope lookup ile recovery ekle.
  - [x] HD index allocation'i transaction/lock disina tasima; index reservation ancak successful wallet insert ile anlamli olsun.

- [x] Task 3: Chain/token validation'i wallet mutation'dan once tamamla (AC: 1, 6, 7)
  - [x] `constants.IsSupportedChainID` kontrolunu ve `asset.Registry` validation'ini wallet creation oncesinde calistir.
  - [x] Native asset default davranisini koru: symbol bos ise chain native asset registry'den bulunabiliyorsa kullan.
  - [x] Non-native token'larda Story 1.2 ile uyumlu davran: `GetBySymbol` ambiguity yaratacaksa token identifier zorunlu olsun veya exact token lookup kullanilsin.
  - [x] Disabled/unsupported chain-token request'inde `v1Err` envelope ile 400 don ve wallet create, HD index mutation, address ownership yazimi yapma.

- [x] Task 4: Trust Wallet Core ve fallback provider guard'ini address dondurmeden uygula (AC: 4, 5, 6, 7)
  - [x] Wallet derivation icin mevcut `blockchain.ChainFactory.CreateHDWallets` ve chain `CreateHDWallet` path'ini kullan; yeni wallet derivation library ekleme.
  - [x] `blockchain/walletcore/provider_fallback.go` build tag path'i address derivation icin error donduruyor; bu davranisin static wallet issuance tarafinda address dondurmeden fail ettigini test et.
  - [x] `WalletRepo.Create` yeni wallet yaratirken required chain address eksikse rollback ediyor; bu fail-fast davranisi bozma.
  - [x] Existing wallet backfill path'inde `EnsureAllAddresses` silent skip davranisini static response icin telafi et: requested chain address bos ise success response dondurme.
  - [x] Production custody iddiasi yapma; NFR4/AD-12 geregi real-funds custody external signer ve operational gates'e bagli kalir.

- [x] Task 5: Partner/dealer surfaces, docs ve tests (AC: 1, 4, 7)
  - [x] `/api/v1/payment/static-address` create ve `/api/v1/payment/static-addresses` list response'larini product/token metadata ile uyumlu hale getir.
  - [x] Mevcut dealer/admin wallet yuzeyleriyle cakisma yaratma; portal action bu story'de API-backed static issuance olarak ele aliniyorsa story completion notes'ta acik yaz.
  - [x] Swagger comments ve public struct'lar degisirse `swag init -g main.go -o docs` calistir ve `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` regenerate et.
  - [x] Handler/helper testleri ve repository-level testler ekle; live network veya real TWC dependency gerektirmeyen deterministic fake chain kullan.
  - [x] Targeted tests: `go test ./api/handlers ./repositories ./types ./blockchain`.
  - [x] Full validation: `go test ./...` ve `go vet ./...`.
  - [x] `bmad-dev-story` geregi Dev Agent Record, Completion Notes, File List, Change Log ve story status alanlarini guncelle.

### Review Findings

- [x] [Review][Patch] Require asset registry before static wallet scope validation [api/handlers/v1api.go:1507] - Static address scope resolution could bypass chain/token validation when the registry was nil; it now fails before wallet mutation and has regression coverage.
- [x] [Review][Patch] Reject unregistered requested chain before wallet creation [api/handlers/v1api.go:761] - Static wallet create could reach wallet creation even when the requested supported chain was not registered in the active chain factory; it now rejects before mutation and has regression coverage.
- [x] [Review][Patch] Update static-address OpenAPI description for product/token scope [api/handlers/v1api.go:724] - The endpoint description still said same `user_id` and chain always return the same address; it now documents the full domain/product/user/chain/symbol/token scope and Swagger was regenerated.

## Dev Notes

### Current Implementation Snapshot

- `api/handlers/v1api.go` icinde `HandleV1PaymentStaticAddressCreate` mevcut. Signed v1 domain auth kullaniyor, `types.V1StaticAddressRequest` bind ediyor, `user_id` ve `chain_id` zorunlu tutuyor, unsupported chain'i reject ediyor.
- Static address handler symbol bos ise registry'den native asset aliyor. Symbol varsa `AssetRegistry.GetBySymbol(chainID, symbol)` ile validate ediyor. Bu non-native token'larda ambiguity yaratabilir; Story 1.2 create-time selected asset icin non-native token identifier zorunlu hale getirdi.
- Handler product scope'u su an `static:<chain_id>:<SYMBOL>` olarak kuruyor. Story AC'si tenant/domain/product/user scope ister. Mevcut product scope chain+symbol bazli oldugu icin token veya explicit product ihtiyaci varsa product id formatini bilincli ve backwards-compatible guncelle.
- Handler `WalletRepo.Create` cagiriyor ve `v1StaticAddressResponse` ile tek chain address'i donduruyor. Response helper su an `product_id`, `chain_id`, `token`, `created_at` dondurmuyor.
- `types.V1StaticAddressRequest` su an `user_id`, `chain_id`, `symbol`, `label` alanlarina sahip. `token` yok.
- `WalletRepo.Create`:
  - `WalletParams.Validate` ile merchant/domain/product/user validation yapar.
  - Domain'in merchant'a ait oldugunu kontrol eder.
  - `pg_advisory_xact_lock(hashtext("wallet-hd-index:<merchant>:<domain>"))` ile HD index allocation'i serialize eder.
  - Existing wallet'i `merchant_id + domain_id + product_id + user_id` scope'unda arar.
  - Existing bulunursa transaction commit eder, `EnsureAllAddresses` cagirir ve `FindByID` ile dondurur.
  - New wallet icin `COALESCE(MAX(hd_address_id),0)+1` hesaplar, `ChainFactory.CreateHDWallets` cagirir, required chain address'leri eksikse rollback eder, sonra `models.Wallet` yaratir.
- `models.Wallet` unique indexes:
  - `ux_wallet_owner`: `merchant_id`, `domain_id`, `product_id`, `user_id`.
  - `ux_wallet_hd`: `hd_account_id`, `hd_address_id`, `merchant_id`, `domain_id`.
  - Per-chain address columns unique indexed.
- `repositories.WalletAddressForChainID` selected chain address okumak icin exported helper sunuyor. Handler tarafinda duplicate switch yazmak yerine bunu kullanmak tercih edilir.
- `EnsureAllAddresses` blockchains nil ise nil donduruyor ve per-chain derivation hatalarini loglayip devam ediyor. Static create response requested address bos ise success dondurmemeli.
- `blockchain/walletcore/provider_trustwalletcore.go` default build path'te Trust Wallet Core binding kullanir. `provider_fallback.go` sadece `walletcorefallback` build tag ile devreye girer ve `DeriveWallet`/`Sign` icin fail-fast error dondurur.
- `blockchain.ChainFactory.CreateHDWallets` registered chain'lerin `CreateHDWallet` methodunu cagirir ve per-chain errors map dondurur.
- `api/handlers/v1api_test.go` icinde mevcut response helper testleri var: `TestV1StaticAddressResponseReturnsSelectedChainAddress` ve `TestV1StaticWalletListItemParsesProductScope`.
- Dealer portalda su an reserve wallet provision/fill/list yuzeyleri var; dogrudan static address create formu gorunmuyor. Bu story API-backed partner/dealer action'i kapsayabilir, ama yeni UI eklenirse `views/` ve server-rendered stack korunmali.

### Architecture Compliance

- AD-2: Merchant checkout/static-address ve exchange user-wallet yuzeyleri ayni Wallet, Deposit, Ledger, Webhook boundary'lerini kullanmali; duplicate money core olusturma.
- AD-5 / FR10: Money-affecting write path'leri idempotent ve duplicate-safe olmali. Static wallet icin semantic idempotency scope `tenant/domain/product/user` plus selected asset identity'dir.
- AD-10: Architecture ismi tenant/domain; current code merchant/domain kullanir. Yeni logic merchant-only varsayimina baglanmamali.
- AD-12 / NFR4: Static wallet address generation, production custody readiness anlami tasimaz. Mnemonic/private key app DB/log/response'a cikmamali; production custody external signer ve launch gates ister.
- Project context: V1 mutating endpoint HMAC auth behavior Story 1.1'den gelir. V1 error envelope `{"result":"error","message":"..."}` korunmali.

### Implementation Guardrails

- Yeni wallet sistemi, yeni derivation library, yeni service boundary veya external broker ekleme.
- `WalletRepo.Create` idempotency/locking modelini bypass etme.
- Chain/token validation'i wallet mutation'dan once yap.
- Existing wallet tekrarinda requested chain address bos ise basarili response verme; fail-safe hata don veya address backfill'i gercekten basarili hale getir.
- Fallback provider error'unu yutma; placeholder veya empty address dondurme.
- Testlerde live network, real RPC veya production mnemonic kullanma. Deterministic fake chain/factory veya in-memory handler boundaries kullan.
- Story 1.1 signed auth ve Story 1.2 selected asset/idempotent payment create davranisini bozma.
- Public struct veya Swagger comment degisirse docs regenerate et.

### Previous Story Intelligence

- Story 1.2 public API contract drift'ini yakaladi; Swagger regenerate etmek contract degisikliginin parcasi olmali.
- Story 1.2 non-native asset selection icin token identifier zorunlulugunu ekledi; static wallet issuance da ayni ambiguity riskini tasir.
- Story 1.2 handler testlerinde repository interface extraction ve in-memory fakes kullanildi; DB gerektirmeyen davranislar icin ayni pattern uygundur.
- V1 endpoints signed auth ve error envelope uyumlulugunu korumali; conflict/error path'leri `v1Err` ile donmeli.

### Testing Requirements

- First issuance: valid signed v1 static address request yeni wallet yaratir, selected chain address dondurur, product/user/domain scope dogrudur.
- Repeat issuance: same tenant/domain/product/user/asset request ayni wallet/address dondurur, wallet count ve HD index artmaz.
- Concurrent issuance: same scope icin parallel requests tek wallet yaratir; race sonucu unique violation olsa bile duplicate record kalmaz.
- Unsupported/disabled chain-token: handler 400 v1 error envelope dondurur ve wallet repo/HD derivation cagrilmaz.
- Non-native token ambiguity: token olmadan non-native symbol request deterministic olmayan asset secimine yol acmaz.
- Fallback guard: walletcore fallback veya failing fake chain derivation path'i address dondurmeden error verir.
- Requested address completeness: existing wallet requested chain address bos ise success response uretilmez.
- Tenant isolation: domain A auth'u domain B wallet'ini retrieve/create scope olarak kullanamaz; same user/product different domain farkli wallet olabilir.
- Contract docs: request/response schema degisirse Swagger artifacts guncel olur.
- Validation commands: targeted `go test ./api/handlers ./repositories ./types ./blockchain`, then `go test ./...`, then `go vet ./...`.

### References

- `_bmad-output/planning-artifacts/epics.md` - Story 1.3 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md` - FR3, FR7, FR8, FR10, NFR2, NFR4, NFR7, NFR14, NFR18.
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md` - AD-2, AD-5, AD-10, AD-12, idempotency and production signing conventions.
- `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md` - dealer/merchant portal static wallets and server-rendered UX surfaces.
- `_bmad-output/project-context.md` - V1 auth, tenant isolation, wallet/money safety, testing and docs rules.
- `_bmad-output/implementation-artifacts/1-2-idempotent-payment-session-creation.md` - previous story learnings.

## Project Structure Notes

- V1 static wallet API stays in `api/handlers/v1api.go`; request/response structs stay in `types/v1api.go`.
- Wallet persistence and HD index allocation stay in `repositories/wallet_repo.go` and `models/wallet.go`.
- Chain derivation stays under `blockchain/` and `blockchain/walletcore/`; no new wallet derivation package is needed.
- Dealer/admin UI changes, if any, must stay in existing server-rendered `views/` stack and avoid SPA introduction.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- `go test ./api/handlers ./blockchain` (red phase failed before helper implementation, then passed)
- `go test ./api/handlers ./repositories ./types ./blockchain`
- `swag init -g main.go -o docs`
- `go test ./...`
- `go vet ./...`
- Review fix validation: `go test ./api/handlers ./repositories ./types ./blockchain`
- Review fix validation: `go test ./...`
- Review fix validation: `go vet ./...`

### Completion Notes List

- Extended static address request contract with optional `product_id` and `token`, while preserving legacy default product scope `static:<chain_id>:<SYMBOL>`.
- Added static address scope resolution that validates supported chain before mutation, defaults native assets from registry, requires exact token identity for non-native assets, and rejects unsupported assets with v1 error semantics.
- Kept wallet issuance inside `WalletRepo.Create` and added a small static wallet core helper over the repository boundary for deterministic scope creation, address completeness checks, and testability.
- Added fail-safe requested-chain address validation so static address create cannot return success with an empty selected chain address after fallback/backfill failure.
- Updated static address create/list responses to expose stable product, chain id, token, address, label, and created-at metadata.
- Added tests for native defaulting, non-native token requirement, token/product scope generation, response metadata, empty address fail-safe, concurrent same-scope issuance, domain isolation, and chain factory HD derivation error propagation.
- Regenerated Swagger docs for the public static address request/response contract.
- Dealer/admin server-rendered wallet surfaces were not changed; this story implements the API-backed partner/dealer static issuance path without introducing new UI.
- Code review patch: asset registry is now required before static address scope validation, preventing unsupported token validation bypass when registry wiring is missing.
- Code review patch: static address create now verifies the requested chain is registered in the active chain factory before wallet creation, preventing partial wallet creation for unavailable chain derivation.
- Code review patch: static-address OpenAPI description now matches the product/token-aware deterministic scope.

### File List

- `api/handlers/v1api.go`
- `api/handlers/v1api_test.go`
- `blockchain/factory_test.go`
- `types/v1api.go`
- `docs/docs.go`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `_bmad-output/implementation-artifacts/1-3-deterministic-static-wallet-issuance-for-partner-scopes.md`
- `_bmad-output/implementation-artifacts/1-3-deterministic-static-wallet-issuance-for-partner-scopes-validation.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Story created with PRD, UX, architecture, project-context, previous-story, git-history, and current-code context.
- 2026-06-27: Implemented deterministic static wallet scope resolution, non-native token guard, static address metadata responses, address completeness fail-safe, tests, and Swagger docs.
- 2026-06-27: Addressed code review findings - asset registry required before validation, requested chain readiness checked before wallet mutation, and static-address docs clarified.
