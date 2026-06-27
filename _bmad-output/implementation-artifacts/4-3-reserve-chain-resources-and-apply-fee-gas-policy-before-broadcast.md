---
story_id: "4.3"
story_key: "4-3-reserve-chain-resources-and-apply-fee-gas-policy-before-broadcast"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: in-progress
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 462af372f7768ba7f988bb1c8e466d80d68a4162
---

# Story 4.3: Reserve Chain Resources and Apply Fee/Gas Policy Before Broadcast

Status: in-progress

## Story

Bir operator olarak,
outbound broadcast oncesinde nonce, UTXO, resource ve fee/gas policy sahipliginin alinmasini istiyorum,
boylece concurrent payout, refund ve sweep isleri ayni chain kaynagini yeniden kullanmaz ve blind retry ile fonlari kilitlemez.

## Acceptance Criteria

1. Given an account-based chain outbound transaction is prepared, when signing is requested, then the system reserves nonce or equivalent chain sequence ownership before signing, and concurrent outbound jobs for the same wallet cannot reuse the same nonce.
2. Given a Bitcoin-like outbound transaction is prepared, when coin selection runs, then selected UTXOs are reserved before signing, and concurrent jobs cannot spend the same UTXO set.
3. Given Solana, TRON, EVM, or token-specific fees/resources are required, when the outbound transaction is built, then the system applies chain-specific fee/gas/resource policy instead of fixed unsafe defaults where possible, and missing resources route to policy failure, prefund, or operator action instead of blind broadcast.
4. Given a broadcast is stuck or replacement is needed, when retry logic runs, then it checks existing broadcast state and reconciliation evidence before replacement, and it does not create a second unrelated spend for the same reserved funds.
5. Given chain resource reservation is implemented, when automated tests run, then they cover nonce contention, UTXO contention, resource/gas policy failure, stuck tx replacement guard, and reservation release on terminal failure.

## Tasks / Subtasks

- [ ] Task 1: Ortak chain resource reservation ve policy paketi olustur (AC: 1, 2, 3, 4, 5)
  - [ ] Yeni external dependency eklemeden kucuk bir `services/chainresource` paketi ekle.
  - [ ] Account-chain nonce reservation icin chain, wallet/key-reference ve nonce bazli sahiplik modeli kur; ayni wallet icin concurrent reserve cagrilari ayni nonce'u dondurmemeli.
  - [ ] Bitcoin-like UTXO reservation icin outpoint bazli sahiplik modeli kur; secili UTXO setinin aktif ikinci bir iste kullanilmasi policy hatasi olmali.
  - [ ] Solana/TRON gibi nonce yerine recent blockhash/block ref/resource kullanan chain'ler icin wallet bazli sequence/resource lease ekle; ayni wallet icin build/sign/broadcast kritik bolumunu serialize et.
  - [ ] Reservation sonucunda terminal pre-broadcast failure icin release, broadcast success icin consume/commit ve stuck replacement icin existing reservation reuse/guard API'leri olsun.
  - [ ] Hatalar operator icin actionable olsun ve raw tx, private key, mnemonic, signature veya full payload icermesin.

- [ ] Task 2: EVM native/ERC-20/prefund path'lerini nonce ve gas policy ile gate et (AC: 1, 3, 4, 5)
  - [ ] `blockchain/chains/evm_transfer.go` icinde `PendingNonceAt` sonucu dogrudan kullanmak yerine shared nonce reservation kullan.
  - [ ] Reservation private-key signing ve Trust Wallet Core input olusmadan once alinmali; broadcast basarisiz olursa pre-broadcast/broadcast-uncertain ayrimiyla release veya consume karari verilmelidir.
  - [ ] Native ve ERC-20 transferlerde gas price/gas limit policy validation ekle; zero/negative price, env cap asimi veya eksik gas policy blind broadcast'e gitmemeli.
  - [ ] ERC-20 icin sabit gas limit varsayimi policy tarafindan isimlendirilmis, cap'lenmis ve testlenmis olsun; mumkunse `EstimateGas` yolu kullanilsin, degilse explicit fallback policy olarak gorunsun.
  - [ ] EVM gas prefund akisi ayni nonce reservation ve gas policy'den gecsin; recursive signer guard veya duplicate reservation problemi olusmasin.

- [ ] Task 3: Bitcoin send/sweep path'lerini UTXO reservation ve fee policy ile gate et (AC: 2, 3, 4, 5)
  - [ ] `blockchain/chains/bitcoin_transfer.go` coin selection sonrasi secilen confirmed UTXO setini signing oncesinde reserve et.
  - [ ] Reserved outpoint'ler broadcast success sonrasi consumed kalmali; private-key decode/signing veya broadcast oncesi terminal hata olursa release edilmeli.
  - [ ] Fee rate sabiti production policy olarak kalmasin; env override ve validation ile minimum/maximum sat/vbyte guard'i ekle.
  - [ ] RBF/replacement icin ayni reserved outpoint setini bilmeden ikinci unrelated spend olusturmayi engelleyen guard/test ekle.

- [ ] Task 4: Solana ve TRON resource/sequence policy'lerini ekle (AC: 1, 3, 4, 5)
  - [ ] Solana lamport ve SPL transferlerinde blockhash fetch, tx build, sign ve broadcast bolumu wallet bazli sequence/resource lease altinda calissin.
  - [ ] Solana fee/prefund policy; sabit 5000 lamport fee varsayimini isimlendirilmis policy ile validate etsin ve priority fee bilgisi yoksa operator-action failure veya explicit fallback olarak gorunsun.
  - [ ] TRON TRX/TRC-20/sweep/prefund path'lerinde block ref, fee limit, bandwidth/energy/resource varsayimlari policy tarafindan validate edilsin.
  - [ ] TRON sabit `feeLimit` ve native sweep fee sabitleri env cap/floor ile policy'ye tasinsin; gecersiz veya eksik policy blind broadcast'e gitmesin.

- [ ] Task 5: Stuck/retry guard ve mevcut worker akislariyla uyumu koru (AC: 4, 5)
  - [ ] `main.go` sweep worker retry davranisinda tx hash veya broadcast-uncertain state varsa yeni unrelated tx yaratmadan once existing broadcast/reconciliation evidence kontrolu zorunlu olsun.
  - [ ] Story 4.1 ledger hold ve Story 4.2 signer boundary davranisini gevsetme; yeni resource gate ledger hold'dan sonra, signer private-key access'ten once gelmeli.
  - [ ] Reconciliation pattern'i reuse et: stuck, broadcast-uncertain veya reservation mismatch durumunda scoped reconciliation job ac veya existing job'a dedupe et.

- [ ] Task 6: Test ve validation kanitlarini ekle (AC: 1, 2, 3, 4, 5)
  - [ ] `services/chainresource` unit testleri: nonce contention, UTXO contention, sequence lease contention, release on terminal failure, consume on broadcast success, replacement guard.
  - [ ] EVM testleri: ayni wallet icin iki concurrent nonce reservation ayni nonce'u kullanamaz; gas cap/policy failure signing oncesi durur.
  - [ ] Bitcoin testleri: ayni outpoint seti ikinci kez reserve edilemez; release sonrasi terminal pre-broadcast failure tekrar denenebilir; fee policy cap/floor calisir.
  - [ ] Solana/TRON testleri: wallet resource lease contention ve invalid fee/resource policy path'leri raw signing/broadcast oncesi fail eder.
  - [ ] Sweep/retry testleri: stuck veya tx hash sahibi job ikinci unrelated spend uretmez ve reconciliation-first davranir.
  - [ ] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/chainresource ./blockchain/chains`.
  - [ ] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [ ] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.

- [ ] Task 7: Story record ve completion evidence'i tamamla (AC: 1, 2, 3, 4, 5)
  - [ ] Dev Agent Record, Debug Log References, Completion Notes, File List, Change Log ve status alanlarini guncelle.
  - [ ] Tum task'ler ve validation gecmeden `sprint-status.yaml` icindeki story status'unu `review` yapma.

## Dev Notes

### Current Implementation Snapshot

- Story 4.1 ledger holds are now mandatory for withdrawal, refund, payout, recover-funds and sweep paths. Do not re-open direct unheld signing/broadcast paths.
- Story 4.2 added `services/signer` and `blockchain/chains/signer_policy.go`. Existing transfer code now calls `authorizeWalletSigning` before local signing in EVM, Bitcoin, Solana and TRON paths.
- The current EVM implementation still calls `client.PendingNonceAt(ctx, from)` inside `evmSendNativeWithClient` and `evmSendERC20WithClient`, then signs immediately. Two concurrent jobs for the same wallet can fetch the same pending nonce.
- EVM gas policy is still mostly fixed/fallback based: native gas limit `21000`, ERC-20 gas limit `65000`, `SuggestGasPrice`, and prefund env values `EVM_GAS_THRESHOLD_WEI` / `EVM_GAS_PREFUND_WEI`.
- Bitcoin UTXO selection is local in `sendTo` and `SweepTo`; selected confirmed UTXOs are passed straight into Trust Wallet Core/manual signing and are not reserved before signing. Fee rate is fixed by `btcFeeRateSatPerVByte = 10`.
- Solana lamport/SPL paths fetch latest blockhash and sign/broadcast without a wallet-level resource lease. Native fee is `solanaTransferFeeLamports = 5000`; prefund env values are threshold/prefund amount only.
- TRON paths fetch block ref, build raw tx, sign and broadcast without a wallet-level resource lease. TRC-20 fee limit is fixed at `50_000_000`; native sweep fee margin is fixed at `1_100_000`.
- Sweep worker in `main.go` now preserves ledger safety and reconciliation for some uncertain states, but retries must not produce a second unrelated spend once a broadcast hash or broadcast-uncertain evidence exists.

### Architecture And Product Guardrails

- AD-7 is the controlling decision: no outbound transaction may be signed before ledger hold/reservation succeeds and chain-specific ownership is acquired: nonce reservation for account chains, UTXO reservation for Bitcoin-like chains, and resource/gas policy where applicable.
- AD-6 still applies: signer boundary remains mandatory for production custody. Chain resource policy must run before private-key signing and must not leak secrets.
- AD-11 applies to stuck or uncertain broadcasts: open/update scoped reconciliation jobs instead of blind retries.
- FR20 owns chain-specific nonce, UTXO, resource/gas reservation before signing.
- FR23 owns gas prefund and chain-specific funding sub-job idempotency/concurrency policy.
- FR35 owns fee/gas policy across EVM EIP-1559/gas estimation, ERC-20 gas, Bitcoin fee/RBF/CPFP, Solana priority fee/blockhash retry, and TRON bandwidth/energy accounting.
- NFR2/NFR5/NFR14 require idempotent money movement, retry/crash safety, and concurrency/regression tests.

### Implementation Boundaries

- Do not add a new broker, external queue, service split, SPA, or chain SDK.
- Prefer a small shared policy/reservation package plus narrow call-site changes in chain transfer files.
- Keep reservations non-secret: keys may include chain, wallet address/key reference, nonce, outpoint, resource kind, intent/job/correlation id. Never include private key, mnemonic, raw signed tx, raw signature, or full request payload.
- Because current chain methods do not receive repositories, this story may start with a process-local monolith reservation manager for in-process concurrency. If durable cross-replica storage is added, it must include schema verification or a migration plan; otherwise explicitly document the remaining multi-replica caveat in completion notes and product-readiness audit.
- Do not change public webhook HMAC behavior, V1 request auth semantics, V1 error envelope shape, or ledger balance authority.
- Do not implement a real external signer adapter; Story 4.2 intentionally fails external modes until a provider is selected.

### Previous Story Intelligence

- Story 4.1 introduced strict hold assertions and exact-amount sweep behavior. Chain resource reservation must happen after those holds exist, not as a replacement for ledger holds.
- Story 4.1 review fixed auto-sweep exact-amount broadcast, strict asset validation, success-before-release ordering, dead-letter reconciliation, and strict hold matching. Preserve those semantics in `main.go`.
- Story 4.2 signer guard already blocks production software signing before private-key access. New resource gates should not move signer authorization later than it is today; safest ordering is: validate request/address/amount -> ledger hold exists in caller -> chain resource policy/reservation -> signer policy/private-key access -> sign/build/broadcast -> commit/release reservation.
- Story 3.6 reconciliation jobs provide the pattern for bounded evidence, active dedupe and redaction; reuse that pattern for stuck/broadcast-uncertain resource mismatches.

### Latest Technical Notes

- `go.mod` currently pins go-ethereum `v1.17.2`, btcd `v0.25.0`, gagliardetto/solana-go `v1.12.0`, and OKX TRON SDK pseudo-version. Do not upgrade dependencies for this story.
- go-ethereum `ethclient.EstimateGas` is available but estimation is not guaranteed exact because pending/latest state can change. Treat estimation as a basis for bounded policy, not as proof of success. Source: https://pkg.go.dev/github.com/ethereum/go-ethereum/ethclient
- Solana official RPC `getRecentPrioritizationFees` returns recent priority-fee samples and nodes cache up to 150 blocks. If the current Go SDK path does not expose it cleanly, fail explicitly or leave a named fallback policy rather than pretending fixed priority fee support exists. Source: https://solana.com/docs/rpc/http/getrecentprioritizationfees
- TRON resource accounting is bandwidth/energy based; accounts receive free daily bandwidth and smart-contract calls consume Energy. Fixed `fee_limit` must be treated as a policy cap, not a generic gas guarantee. Sources: https://developers.tron.network/docs/resource-model and https://tronprotocol.github.io/documentation-en/mechanism-algorithm/resource/
- Bitcoin Core `estimatesmartfee` estimates fee per kilobyte for a confirmation target and RBF replacement must spend at least one same input with a higher fee. Current repo uses HTTP explorer-style UTXO/broadcast endpoints, so this story should not require bitcoind RPC unless the adapter already exists. Sources: https://developer.bitcoin.org/reference/rpc/estimatesmartfee.html and https://bitcoincore.org/en/faq/optin_rbf/

### Likely Files To Touch

- `services/chainresource/*` (NEW)
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/evm_transfer_test.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/bitcoin_transfer_test.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`
- `blockchain/chains/solana_tron_balance_test.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/tron_transfer_test.go`
- `blockchain/chains/signer_policy.go`
- `main.go`
- `main_sweep_reservation_test.go`
- `docs/product-readiness-audit.md`
- `_bmad-output/implementation-artifacts/4-3-reserve-chain-resources-and-apply-fee-gas-policy-before-broadcast.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Project Structure Notes

- Repo Go modular monolith olarak kalir; module path `core`.
- Shared code icin `services/...` paketleri mevcut pattern'dir (`services/signer`, `services/reconciliation`, `services/txrescan`).
- Chain-specific transfer code currently lives under `blockchain/chains`; keep helpers close to existing files unless shared policy belongs in `services/chainresource`.
- Tests should stay deterministic and avoid live network calls. Prefer unit tests around reservation manager, fee policy helpers and source-contract guards.
- Current worktree has unrelated UI template/CSS changes. Do not revert or include those in story implementation ownership.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.3.
- PRD FR20/FR23/FR35 and NFR2/NFR5/NFR14: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-6/AD-7/AD-11: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Solution design outbound flow: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`.
- Readiness context: `_bmad-output/planning-artifacts/implementation-readiness-report-2026-06-27.md`.
- Project context: `_bmad-output/project-context.md`.
- Previous stories: `_bmad-output/implementation-artifacts/4-1-reserve-ledger-holds-before-outbound-money-movement.md`, `_bmad-output/implementation-artifacts/4-2-enforce-external-signer-boundary-and-production-software-signer-guard.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 4.3 with PRD FR20/FR23/FR35, architecture AD-6/AD-7/AD-11, current EVM nonce, Bitcoin UTXO, Solana blockhash, TRON resource, sweep retry, Story 4.1 ledger hold and Story 4.2 signer-boundary analysis.

### Completion Notes List

### File List

- `_bmad-output/implementation-artifacts/4-3-reserve-chain-resources-and-apply-fee-gas-policy-before-broadcast.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Created ready-for-dev story with chain resource reservation scope, current implementation snapshot, previous-story guardrails, chain-specific policy tasks, tests, and validation plan.
