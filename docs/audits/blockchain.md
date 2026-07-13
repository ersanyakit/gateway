# Blockchain Architecture Audit

Tarih: 2026-06-28

Rol: Senior Blockchain Architect

Kapsam:
- Repository'deki Go kodu incelendi.
- `third_party/trustwallet/wallet-core/samples/go/**` altındaki generated/proto/sample kod platform implementasyonu olarak kabul edilmedi. Sadece uygulamanin `blockchain/walletcore` wrapper'i uzerinden kullandigi davranis dikkate alindi.
- Bu rapor kaynak kod degisikligi onermez ve kodda olmayan bir ozelligi mevcutmus gibi varsaymaz.

## 1. HD Wallet

Mevcut implementasyon:
- HD wallet kaynagi `MNEMONIC_PHRASE` ortam degiskenidir. `BaseChain.GetMnemonicForPath` signer policy kontrolu yapar, sonra mnemonic'i env'den okur ve validate eder.
- `BaseChain.GetDerivedWallet` Trust Wallet Core wrapper'i ile address/private key derive eder ve `WalletDetails` icinde `Address`, `PrivateKey`, `MnemonicPhrase`, `KeyReference`, `DerivationPath`, `SignerMode` dondurur.
- Domain seviyesinde `HDAccountID`, wallet seviyesinde `HDAddressId` tutulur. Merchant reserve wallet `HDAddressId = 0` olarak olusturulur.
- Production modunda software signer bloklanir; external signer modlari taninir ama aktif adapter yoktur, `Authorize` hata dondurur.

Dosya:
- `blockchain/basechain.go`
- `blockchain/walletcore/provider.go`
- `blockchain/walletcore/provider_trustwalletcore.go`
- `blockchain/walletcore/provider_fallback.go`
- `repositories/domain_repo.go`
- `repositories/wallet_repo.go`
- `services/signer/policy.go`

Fonksiyon:
- `BaseChain.GetMnemonicForPath`
- `BaseChain.GetDerivedWallet`
- `walletcore.DeriveWallet`
- `trustWalletCoreProvider.DeriveWallet`
- `DomainRepo.getNextDomainHDIndex`
- `WalletRepo.getNextHDIndex`
- `WalletRepo.Create`
- `WalletRepo.CreateReserveWallet`
- `signer.Authorize`

Eksik:
- External signer/KMS/HSM/MPC adapter kontrati var; gercek provider implementasyonu ve chain-specific external signing henuz yok.
- xpub/public-only derivation yok.
- Mnemonic rotation, seed versioning veya key custody migration akisi yok.
- Derivation metadata'si merkezi ve versioned bir path registry olarak modellenmemis.

Risk:
- Mnemonic ve private key process memory'sine giriyor. `WalletDetails` private key ve mnemonic tasiyor.
- Production'da software signer kapaliysa mevcut derivation/signing yolu calismayabilir; external signer modlari adapter olmadigi icin gercek custody alternatifi saglamiyor.
- Seed/path degisimi veya migration durumunda eski adresleri deterministik olarak yonetmek zorlasir.

Oneri:
- Software signer'i sadece development/test icin tutup production icin gercek external signer adapter implementasyonlarini ekleyin.
- Address derivation'i mumkunse xpub/public derivation uzerinden yapin; private key sadece signing boundary'sinde uretilsin veya hic process'e alinmasin.
- Path scheme, seed/key version ve signer mode bilgilerini kalici ve audit edilebilir bir wallet derivation registry'de saklayin.

## 2. Address Derivation

Mevcut implementasyon:
- EVM ve EVM-compatible chain'ler `m/44'/60'/{hdAccountId}'/0/{hdWalletId}` path'ini kullanir. Ethereum, Base, Arbitrum, Unichain, Avalanche, BNB Chain, Chiliz ve Chiliz Spicy ayni coin type `60` ile adres turetir.
- Bitcoin `m/86'/0'/0'/{hdAccountId}/{hdWalletId}` path'ini kullanir. Trust Wallet Core tarafinda Bitcoin adresi Taproot derivation ile uretilir.
- TRON ve TRON testnet `m/44'/195'/0'/{hdAccountId}/{hdWalletId}` path'ini kullanir.
- Solana `m/44'/501'/{hdAccountId}'/{hdWalletId}'` path'ini kullanir.
- Wallet kaydinda her chain icin kolon bazli adres tutulur. TRON ve TRONTestnet ayni `TronAddress` alanina map edilir.

Dosya:
- `blockchain/basechain.go`
- `blockchain/chains/ethereum.go`
- `blockchain/chains/evm_compatible.go`
- `blockchain/chains/bitcoin.go`
- `blockchain/chains/tron.go`
- `blockchain/chains/solana.go`
- `repositories/wallet_repo.go`
- `workers/indexer/address_index.go`

Fonksiyon:
- `BaseChain.GetDerivedPath`
- `EthereumChain.CreateHDWallet`
- `EVMCompatibleChain.CreateHDWallet`
- `BitcoinChain.CreateHDWallet`
- `TronChain.CreateHDWallet`
- `SolanaChain.CreateHDWallet`
- `WalletRepo.Create`
- `WalletRepo.EnsureAllAddresses`
- `WalletAddressForChainID`
- `AddressIndex.Load`

Eksik:
- Chain/path/version bilgisini zorunlu hale getiren merkezi path registry yok.
- Bitcoin path'i BIP86 account/change/index yerlesiminden farkli kullaniliyor; `hdAccountId` change segmentine yerlestirilmis.
- TRON mainnet/testnet icin ayri adres kolonu veya ayri derivation namespace'i yok.
- Address derivation fallback build'i address turetemiyor; `walletcorefallback` build'inde `DeriveWallet` hata donduruyor.

Risk:
- Bitcoin path semantigi standart wallet tooling ile uyumsuz olabilir.
- TRON mainnet ve testnet ayni adres alanini kullandigi icin testnet/mainnet operasyonel karisikligi olusabilir.
- EVM chain'lerde ayni address'in birden fazla chain'de kullanilmasi privacy ve operational isolation riskidir.
- Yeni chain eklemek wallet schema, lookup, repo ve factory tarafinda cok noktali degisiklik gerektirir.

Oneri:
- Her chain icin explicit `derivation_scheme_version`, `purpose`, `coin_type`, `account`, `change`, `index` metadata'si saklayin.
- TRON mainnet/testnet adreslerini chain-id bazli normalized address tablosunda ayirin.
- Bitcoin path uyumlulugunu BIP86 standardina gore yeniden degerlendirin; degisim gerekiyorsa versioned migration yapin.

## 3. Gap Limit

Mevcut implementasyon:
- Kodda BIP44/BIP86 tarzinda gap-limit discovery veya seed restore address scanning implementasyonu bulunmadi.
- Address uretimi DB-driven calisiyor: domain ve wallet kaydi olustukca `HDAddressId` allocate ediliyor, sonra chain adresleri derive edilip wallet tablosuna yaziliyor.
- Address lookup icin preload mekanizmasi var; varsayilan `-1` tum ownership kayitlarini yukler ve index'i authoritative yapar.

Dosya:
- `repositories/wallet_repo.go`
- `repositories/domain_repo.go`
- `workers/indexer/address_index.go`

Fonksiyon:
- `WalletRepo.getNextHDIndex`
- `WalletRepo.Create`
- `WalletRepo.EnsureAllAddresses`
- `DomainRepo.getNextDomainHDIndex`
- `AddressIndex.Load`
- `addressIndexPreloadLimit`

Eksik:
- Kullanilmamis adres araligi tarama yok.
- Seed restore veya DB kaybi sonrasinda chain uzerinden adres kesfi yok.
- Gap limit degeri, scan cursor'u veya per-chain discovery state yok.

Risk:
- DB'de wallet row'u yoksa, seed ile turetilmis olsa bile adres izlenmez.
- Backfill sadece mevcut wallet row'lari icin adres turetir; harici veya kayip index araligini bulmaz.
- `ADDRESS_INDEX_PRELOAD_LIMIT` unset/default `-1` iken eksiksiz ve authoritative bellek index'i yuklenir. `0` veya sonlu bir limit kullanilirsa index authoritative sayilmaz ve listener'lar false-negative ya da event basina DB sorgusu uretmek yerine fail-closed kalir.

Oneri:
- Urun gereksinimi varsa per-domain/per-chain gap-limit scanner ekleyin.
- Gap scanner yoksa bunu acik bir invariant olarak dokumante edin: "sadece DB'de olusturulmus adresler izlenir".
- Address discovery state'i ve son taranan index'leri kalici tabloda tutun.

## 4. Watcher

Mevcut implementasyon:
- Startup'ta her chain icin listener olusturuluyor ve dispatcher bus'a subscribe ediliyor. Bus subscriber yalniz kayitli asset icin confirmed, pozitif, platform-owned `To` transferini kabul eder; ayni merchant'a ait custody adresleri arasindaki hareketi eler ve ancak bundan sonra chain fact kaydi yapar.
- EVM listener HTTP JSON-RPC polling ile blok tarar; native tx, ERC-20 log ve opsiyonel `trace_block` internal transfer uretir.
- Bitcoin listener Blockstream/mempool.space REST API uzerinden confirmed bloklari ve vout'lari tarar.
- TRON listener gRPC fullnode uzerinden blok ve transaction info tarar; TRC-20 loglari transaction info varsa isler.
- Solana listener finalized slot/block tarar. System transferleri uretir; SPL transferlerinde token-account owner/mint/program/decimals bilgisini pre/post token balance metadata'sindan strict olarak cozer. Diger program call adaylari sifir tutar filtresinde DB persistence oncesi elenir.
- Listener'lar `ChainState.LastProcessedBlock` ve `LastConfirmedBlock` alanlarini gunceller.

Dosya:
- `main.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`
- `blockchain/basechain.go`

Fonksiyon:
- `startChainInfrastructure`
- `handleChainIndexerEvent`
- `recordChainFactObservation`
- `evm.RpcListener.catchUp`
- `evm.RpcListener.processBlock`
- `bitcoin.RpcListener.catchUp`
- `bitcoin.RpcListener.processBlock`
- `tron.RpcListener.catchUp`
- `tron.RpcListener.processBlock`
- `solana.RpcListener.catchUp`
- `solana.RpcListener.processSlot`
- `BaseChain.StartWorkers`
- `BaseChain.Work`

Eksik:
- Mempool watcher yok.
- Listener seviyesinde aktif reorg rollback/replay yok.
- Listener'lar RPC tarafinda bloktaki aday olaylari genel olarak tarar; DB persistence sinirinda eksiksiz in-memory address index ile wallet ownership filtresi uygulanir. Index authoritative degilse listener altyapisi fail-closed kalir.
- EVM WebSocket watcher startup path'inde kullanilmiyor. `workers/listeners/chiliz` altinda WS listener var, fakat `startChainInfrastructure` default path'te EVM listener'i seciliyor.

Risk:
- Full block scanning yuksek hacimli chain'lerde RPC ve CPU maliyetini buyutur; ilgisiz aday olaylar merkezi filtreden sonra DB'ye yazilmaz.
- `LastProcessedBlock` bozuk veya reorglu hale gelirse otomatik geri sarma yok.
- `trace_block` olmayan EVM provider'larda internal transferler atlanabilir; capability/fetch hatasi loglanir, `REQUIRE_EVM_TRACE=true` ise listener checkpoint'i ilerletmez.
- Baslangic state'i yoksa listener varsayilan olarak son safe head civarindan baslar; gecmis bloklar otomatik taranmaz.

Oneri:
- Watcher icin per-chain replay window ve reorg-aware checkpoint tasarlayin.
- Address-interest filtering veya staged raw event pipeline ekleyin.
- EVM icin provider capability kontrolu ve trace gereksinimini chain/asset bazinda explicit hale getirin.

## 5. Mempool

Mevcut implementasyon:
- Kodda mempool subscription, pending tx watcher veya txpool takip implementasyonu bulunmadi.
- Bitcoin balance sorgusu `mempool_stats` dahil ederek balance string'i hesapliyor.
- EVM transferlerde nonce kaynagi olarak `PendingNonceAt` kullaniliyor.
- Sweep hata siniflandirmasinda `"mempool"` string'i broadcast belirsizligi olarak ele aliniyor.

Dosya:
- `blockchain/chains/bitcoin.go`
- `blockchain/chains/evm_transfer.go`
- `main.go`
- `repositories/withdrawal_request_repo.go`

Fonksiyon:
- `BitcoinChain.getBalance`
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `sweepFailureBroadcastUncertain`

Eksik:
- Pending deposit detection yok.
- Pending outbound tx replacement/speed-up/cancel yok.
- Bitcoin mempool UTXO takibi yok; transfer/sweep sadece confirmed UTXO kullaniyor.
- EVM txpool reconciliation yok.

Risk:
- Kullanici pending deposit gorunurlugu alamaz.
- Outbound broadcast belirsizliginde sistem tx'in mempool durumunu aktif dogrulamaz.
- Nonce gap veya stuck transaction durumlari operator/reconciliation isine kalir.

Oneri:
- Mempool izleme gerekiyorsa chain bazli pending tx pipeline'i ekleyin.
- EVM icin tx replacement policy, fee bump ve txpool lookup ekleyin.
- Bitcoin icin unconfirmed UTXO kullanilmamasi bilincli politika ise dokumante edin.

## 6. Confirmations

Mevcut implementasyon:
- Confirmation requirement env ile override edilebilir; default degerler Bitcoin 3, Solana 1, TRON/TRONTestnet 20, diger chain'ler 12.
- EVM watcher 12 blok geriden, Bitcoin 6 blok geriden, TRON 2 blok geriden isler; Solana finalized commitment kullanir.
- Deposit service chain state'e gore confirmations hesaplar ve `fact.Finalized = confirmations >= required` olarak gunceller.
- Transaction finality worker pending transaction'lari `MarkFinality` ile pending/confirmed durumuna ceker.

Dosya:
- `main.go`
- `services/deposits/service.go`
- `repositories/transaction_repo.go`
- `repositories/deposit_repo.go`
- `repositories/chain_fact_repo.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`

Fonksiyon:
- `chainConfirmationRequirement`
- `transactionConfirmations`
- `finalizePendingTransactions`
- `deposits.Service.factWithFinality`
- `confirmationsForBlock`
- `TransactionRepo.MarkFinality`
- `depositStatusForFinality`
- `ChainFactRepo.Record`
- `ChainFactRepo.RecordOrUpdate`

Eksik:
- Chain-specific finality semantics, fork choice veya probabilistic finality modeli yok; sadece block height farki kullaniliyor.
- `ChainFactRepo.Record` duplicate event icin confirmations/finality update etmez; sadece `RecordOrUpdate` bunu yapar.
- Confirmation policy ve listener safe lag degerleri ayni merkezi config'te tutulmuyor.

Risk:
- Finality ve watcher safe lag ayarlari birbirinden kopuk oldugu icin chain bazli operasyonel hata riski var.
- Duplicate chain fact'lerin normal listener akisiyle confirmation metadata'si guncellenmez; deposit service bunu chain state'ten telafi ediyor, fakat chain_fact kaydi kendi basina stale kalabilir.
- TRON watcher 2 blok geriden event uretirken finality default 20; bu dogru olabilir ama policy acik dokumante edilmezse erken event/finality beklentisi karisir.

Oneri:
- Per-chain finality policy dosyasi/registry olusturun.
- Chain fact confirmation update davranisini listener ve rescan yollarinda tutarli hale getirin.
- Finality hesaplamasini block hash/fork state ile birlikte modelleyin.

## 7. Reorg

Mevcut implementasyon:
- Transaction repository block identity degisimlerini ve canonical block conflict'lerini yakalayacak kod iceriyor.
- Conflict durumunda transaction reorged yapiliyor, ledger reversal post ediliyor, payment/deposit/chain_fact reorged isaretleniyor, ilgili pending/processing/failed sweep job'lar dead-letter oluyor ve reconciliation job aciliyor.
- Block tablosunda canonical/reorged status alanlari kullaniliyor.

Dosya:
- `repositories/transaction_repo.go`
- `repositories/chain_fact_repo.go`
- `repositories/deposit_repo.go`
- `models/block.go`
- `main.go`
- `services/deposits/service.go`

Fonksiyon:
- `TransactionRepo.Create`
- `observeCanonicalBlockWithDB`
- `markBlockHashConflictsWithDB`
- `markParentHashConflictsWithDB`
- `markBlockRangeTransactionsReorgedWithDB`
- `markTransactionsReorgedWithDB`
- `ChainFactRepo.MarkReorgedByTransactionWithDB`
- `DepositRepo.MarkReorgedByTransactionWithDB`
- `transactionParamFromChainFact`
- `chainFactMetadata`

Eksik:
- Listener seviyesinde canonical chain reorg detector ve rollback yok.
- ChainState'i reorg noktasina geri alma ve replay mekanizmasi yok.
- Deposit service tarafinda `transactionParamFromChainFact` parent hash okumaya calisiyor, ancak `chainFactMetadata` sadece `from` ve `to` alanlarini parse ediyor; `parent_hash` metadata'si transaction'a tasinmiyor.
- Reorg detection, yeni bir conflicting transaction/fact gorulmesine bagli; sadece block header zinciri dogrulayan bagimsiz worker yok.

Risk:
- Parent hash conflict detection chain_fact -> transaction yolunda pratikte devre disi kalabilir.
- Bir reorg deposit transaction'ini ortadan kaldirir ama sistem yeni conflicting event gormezse eski kayit kalabilir.
- Reorg sonrasi sweep/payments tarafinda reconciliation acilsa da operasyon manuel cozum gerektirebilir.

Oneri:
- `chainFactMetadata` parent hash'i de parse etmeli ve transaction param'a tasimali.
- Listener'lara kucuk bir rolling replay window ve canonical parent hash kontrolu ekleyin.
- ChainState rollback ve deterministic replay akisini repository seviyesinde idempotent hale getirin.

## 8. Sweeping

Mevcut implementasyon:
- Finalized deposit icin durable `SweepJob` olusturuluyor. Reserve wallet merchant bazinda `_reserve_` domain ve `HDAddressId = 0` ile kullaniliyor.
- Sweep worker due job'lari claim eder, ledger sweep hold olusturur, user wallet'i yeniden derive eder, reserve wallet adresine transfer yapar, basarida job'u succeeded yapar ve ledger sweep release post eder.
- Token deposit'lerde gas prefund denenir; sonra `WithdrawToken` ile deposit amount reserve adrese gonderilir.
- Native TRON deposit'lerde `SweepTo` ile full native sweep kullanilir. Diger native chain'lerde `Withdraw` ile sadece transaction amount gonderilir.
- Chain seviyesinde EVM/BTC/Solana full native `SweepTo`, EVM/TRON/Solana token `SweepERC20To` fonksiyonlari var; auto sweep deposit path'i bunlari her chain icin kullanmiyor.

Dosya:
- `main.go`
- `repositories/sweep_job_repo.go`
- `repositories/ledger_repo.go`
- `services/deposits/service.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana_transfer.go`

Fonksiyon:
- `enqueueSweepJob`
- `executeAutoSweepDeposit`
- `executeAutoSweepDepositWithJob`
- `processSweepJobs`
- `scheduleMissingFinalizedSweepJobs`
- `SweepJobRepo.EnqueueForTransaction`
- `SweepJobRepo.ClaimDue`
- `SweepJobRepo.MarkSucceeded`
- `SweepJobRepo.MarkFailed`
- `SweepJobRepo.MarkBroadcastUncertain`
- `deposits.Service.enqueueFinalizedSweepJob`
- `evmSweepNativeTo`
- `evmSweepERC20To`
- `BitcoinChain.SweepTo`
- `TronChain.SweepTo`
- `TronChain.SweepERC20To`
- `SolanaChain.SweepTo`
- `SolanaChain.SweepERC20To`

Eksik:
- Sweep tx finality tracking yok; ledger release broadcast basarisindan hemen sonra post ediliyor.
- EVM/BTC/Solana native deposit auto sweep full balance sweep yapmiyor; sadece deposit amount transfer ediyor.
- Token auto sweep deposit amount ile sinirli; full token consolidation kullanilmiyor.
- Broadcast sonrasi stuck sweep tx icin chain bazli confirmation/replacement workflow yok.

Risk:
- Sweep tx broadcast edilip chain'de fail/reorg olursa ledger release on-chain gerceklikten once yazilmis olabilir.
- User deposit adreslerinde dust, change veya ekstra bakiye kalabilir.
- Gas prefund sonrasi sadece 5 saniye bekleniyor; prefund finality/availability garanti degil.
- Broadcast belirsizligi dead-letter ve reconciliation'a dusuyor; otomatik on-chain kesinlestirme yok.

Oneri:
- Sweep tx'leri de normal transaction/finality pipeline'ina alin; ledger release'i finality sonrasi post edin.
- Native ve token sweep politikasini chain bazinda explicit yapin: amount sweep mi, full consolidation mi.
- Broadcast belirsizligi icin tx hash lookup, replacement ve retry stratejisi ekleyin.

## 9. Gas Management

Mevcut implementasyon:
- EVM transferler legacy transaction mode kullanir, `SuggestGasPrice` ile gas price alir. Native gas limit 21000, ERC-20 gas limit 65000 sabittir.
- EVM gas policy sadece gas price pozitifligini ve opsiyonel `EVM_MAX_GAS_PRICE_WEI` cap'ini kontrol eder.
- EVM token sweep icin native balance threshold/prefund env ile belirlenir.
- Bitcoin fee rate env tabanlidir: `BITCOIN_FEE_RATE_SAT_PER_VBYTE`; min/max cap vardir.
- TRON TRC-20 fee limit env tabanlidir; native sweep bandwidth fee tahmini yapar ve fallback fee kullanir.
- Solana transfer fee sabit env degeridir; default 5000 lamports.

Dosya:
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`
- `services/chainresource/policy.go`

Fonksiyon:
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `evmSignNativeWithTrustWallet`
- `evmSignERC20WithTrustWallet`
- `evmPrefundGas`
- `BitcoinFeeRateSatPerVByte`
- `btcEstimateFee`
- `TronTRC20FeeLimitSUN`
- `TronNativeSweepFeeSUN`
- `tronEstimateBandwidthFeeSUN`
- `SolanaTransferFeeLamports`
- `solanaTransferFeeLamports`
- `PrefundGas`

Eksik:
- EVM EIP-1559 `maxFeePerGas` / `maxPriorityFeePerGas` yok.
- EVM gas estimation (`EstimateGas`) yok.
- Fee bump/speed-up/cancel yok.
- Bitcoin dynamic fee estimation ve RBF policy yok.
- Solana priority fee/compute budget/rent dynamic handling yok.

Risk:
- Sabit gas limit veya static fee yogun aglarda basarisiz ya da asiri pahali olabilir.
- Legacy EVM tx mode bazi chain kosullarinda optimal degil.
- Token transferlerde gas limit 65000 her token icin yeterli olmayabilir.

Oneri:
- EVM icin EIP-1559 destekli gas oracle ve `EstimateGas` ekleyin.
- Bitcoin icin mempool fee estimator ve RBF stratejisi ekleyin.
- Solana icin priority fee, compute budget ve ATA/rent maliyetini dinamik hesaplayin.

## 10. Nonce

Mevcut implementasyon:
- EVM nonce reservation process-local `chainresource.Manager` icindeki map'lerle yapilir. Base nonce `PendingNonceAt` ile alinir.
- Nonce broadcast hatasinda da consumed olarak isaretlenir.
- Bitcoin UTXO reservation process-local map ile yapilir; broadcast hatasinda fallback tx id ile consumed isaretlenir.
- TRON ve Solana sequence lease process-local tek aktif sequence olarak modellenir.

Dosya:
- `services/chainresource/manager.go`
- `blockchain/chains/chain_resource_policy.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`

Fonksiyon:
- `chainresource.Manager.ReserveNonce`
- `NonceReservation.Release`
- `NonceReservation.Consume`
- `Manager.ReserveUTXOs`
- `UTXOReservation.Consume`
- `Manager.AcquireSequence`
- `SequenceLease.Consume`
- `chainResourceSequenceLease`
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `BitcoinChain.sendTo`
- `BitcoinChain.SweepTo`
- `TronChain.sendTRX`
- `TronChain.sendTRC20`
- `SolanaChain.sendLamportsWithClient`

Eksik:
- Durable nonce/UTXO/sequence lock yok.
- Multi-instance coordination yok.
- Txpool/on-chain reconciliation ile nonce gap kapatma yok.
- EVM replacement transaction lifecycle yok.

Risk:
- Birden fazla app instance calisirsa ayni nonce veya UTXO paralel kullanilabilir.
- Process restart durumunda reservation state kaybolur; broadcast sonucu belirsiz tx'ler icin tekrar deneme karmasik hale gelir.
- Broadcast hatasinda nonce/UTXO/sequence consumed isaretlenmesi process icinde duplicate'i engeller, fakat tx hic yayilmadiysa nonce gap veya locked UTXO algisi olusabilir.

Oneri:
- Nonce/UTXO/sequence reservation'i DB tabanli lease ve state machine ile kalici hale getirin.
- Broadcast belirsizligi icin chain lookup + retry/replacement workflow ekleyin.
- Multi-instance calisma hedefleniyorsa advisory lock veya row-level lock kullanin.

## 11. Recovery

Mevcut implementasyon:
- Manual tx rescan servisi EVM, Bitcoin, Solana, TRON/TRONTestnet icin transaction hash ile eventleri tekrar okuyup chain fact kaydedebilir ve deposit processing calistirabilir.
- Startup backfill mevcut wallet row'lari icin eksik chain adreslerini turetir ve lookup tablosunu doldurur.
- Sweep worker eksik finalized sweep job'lari tekrar enqueue eder.
- Stale processing sweep job broadcast belirsizligi olarak dead-letter/reconciliation'a alinir.
- Ledger invariant reconciliation ve reserve balance reconciliation worker'lari calisir.

Dosya:
- `services/txrescan/service.go`
- `api/handlers/txrescan.go`
- `main.go`
- `repositories/sweep_job_repo.go`
- `services/reconciliation`
- `repositories/wallet_repo.go`

Fonksiyon:
- `txrescan.Service.Rescan`
- `txrescan.Service.RescanForMerchant`
- `txrescan.Service.recordRescanFact`
- `txrescan.Service.processDepositFact`
- `backfillMissingAddresses`
- `scheduleMissingFinalizedSweepJobs`
- `processSweepJobs`
- `markSweepBroadcastUncertainAndReconcile`
- `startReconciliationWorker`
- `runLedgerInvariantReconciliation`
- `runReserveBalanceReconciliation`

Eksik:
- Otomatik gap-limit recovery yok.
- Otomatik historical rescan/backfill worker yok; listener current state'ten ileri akar.
- Reorg sonrasi otomatik rollback/replay yok.
- Mnemonic restore ile DB address reconstruction disinda chain discovery yok.

Risk:
- Kayip veya atlanmis blok/event operator manual rescan yapmadan yakalanmayabilir.
- Seed/DB restore senaryolarinda sadece DB'deki wallet row'lari geri doldurulur.
- Broadcast belirsizligi ve reorg cozumleri reconciliation uzerinden manuel inceleme gerektirebilir.

Oneri:
- Recovery playbook ve operator tooling'i belirginlestirin.
- ChainState replay window ve manual range rescan endpoint'i ekleyin.
- Seed restore icin gap-limit scanner veya explicit "DB authoritative" recovery modeli tasarlayin.

## 12. Blockchain Abstraction

Mevcut implementasyon:
- `blockchain.Chain` interface wallet derivation, transfer, sweep, prefund, address validation, worker lifecycle ve batch balance fonksiyonlarini tek interface'te topluyor.
- `BaseChain` ortak RPC/env/ranker, worker lifecycle ve derivation helper'lari sagliyor; chain action method'lari default `not implemented`.
- `ChainFactory` chain name -> implementation map'i ve alias destegi sagliyor.
- EVM-compatible chain'ler ortak helper'lar uzerinden transfer/sweep yapar.

Dosya:
- `blockchain/basechain.go`
- `blockchain/factory.go`
- `application/configuration/chains.go`
- `blockchain/chains/evm_compatible.go`
- `main.go`
- `services/txrescan/service.go`
- `repositories/wallet_repo.go`

Fonksiyon:
- `Chain` interface
- `BaseChain.RPCs`
- `BaseChain.CreateHDWallet`
- `ChainFactory.RegisterChain`
- `ChainFactory.GetChain`
- `ChainFactory.GetChainByID`
- `ChainFactory.CreateHDWallets`
- `NewChainFactory`
- `startChainInfrastructure`
- `txrescan.Service.rescan`

Eksik:
- Wallet derivation, transfer, watcher ve balance capability'leri ayri interface'lere bolunmemis.
- Chain capability registry yok.
- Chain-specific davranislar abstraction disina siziyor: `main.go` TRON native sweep ayrimi, `txrescan` chain switch'i, wallet repo chain kolon mapping'i.

Risk:
- Yeni chain eklemek yuksek blast radius olusturur.
- Interface cok genis oldugu icin chain'in desteklemedigi fonksiyonlar runtime error veya no-op seklinde kalabilir.
- Kodda chain-family bilgisi daginik oldugu icin policy tutarsizligi olusabilir.

Oneri:
- `WalletDeriver`, `Broadcaster`, `Watcher`, `FeeManager`, `BalanceProvider`, `SweepProvider` gibi capability interface'leri ayirin.
- Chain config/capability registry'yi tek kaynak haline getirin.
- Unsupported operation'lari compile-time veya startup validation ile yakalayin.

## 13. Node Abstraction

Mevcut implementasyon:
- `BaseChain.RPCs` env ve default endpoint'leri birlestirir; global `RPCURLRanker` varsa siralamayi provider health sonucuna gore degistirir.
- Provider health servisi EVM, Bitcoin, Solana ve TRON endpoint'lerini probe eder; stale/inconsistent/unhealthy durumlarini snapshot olarak saklar ve URL siralamasinda kullanir.
- Listener ve transfer kodlari kendi HTTP/gRPC/ethclient cagrilarini dogrudan yapar.

Dosya:
- `blockchain/basechain.go`
- `services/providerhealth/service.go`
- `repositories/provider_health_repo.go`
- `main.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/tron_transfer.go`

Fonksiyon:
- `BaseChain.RPCs`
- `SetRPCURLRanker`
- `providerhealth.Service.RunOnce`
- `providerhealth.Service.discoverTargets`
- `providerhealth.Service.probe`
- `FinalizeSnapshots`
- `RankURLs`
- `installProviderHealthRanker`
- `runProviderHealthCheck`
- Listener `rpcCall` / `get` / gRPC client functions

Eksik:
- Ortak node client interface'i yok.
- Method capability modeli yok; ornegin EVM `trace_block` destegi provider health seviyesinde bilinmiyor.
- Quorum read veya cross-provider block hash verification listener akisi icinde yok.
- WebSocket-based subscription startup path'inde genel olarak kullanilmiyor.

Risk:
- Provider health URL siralasa da her chain method'unun gercek capability'si ayrica garanti edilmiyor.
- Bir provider method seti eksikse event tipi kaybi olabilir.
- Dogrudan HTTP/gRPC kullanimi retry, timeout, rate-limit ve observability davranisini dagitir.

Oneri:
- Chain-family bazli node client abstraction olusturun.
- Provider capability probe'lari ekleyin: EVM receipts batch, trace, archive depth, Solana finalized blocks, TRON txinfo.
- Listener'lar icin provider quorum veya en azindan head hash consistency guard'i ekleyin.

## 14. Multi-chain Design

Mevcut implementasyon:
- Chain ID ve adlari `constants/chains.go` icinde sabit.
- Chain registration `application/configuration/chains.go` icinde yapiliyor: Solana, Ethereum, TRON, TRON testnet, Bitcoin, Avalanche, BNB Chain, Chiliz, Chiliz Spicy, Base, Arbitrum, Unichain.
- EVM-compatible chain'ler ortak `EVMCompatibleChain` ve `evm_transfer.go` helper'larini paylasiyor.
- Wallet modeli chain basina kolon tutuyor; lookup tablosu chain-id/adres bazli ek index sagliyor.
- Asset registry chain ve token bazli kullaniliyor.

Dosya:
- `constants/chains.go`
- `application/configuration/chains.go`
- `models/wallet.go`
- `models/wallet_address_lookup.go`
- `repositories/wallet_repo.go`
- `workers/indexer/address_index.go`
- `blockchain/chains/evm_compatible.go`
- `asset`

Fonksiyon:
- `AllChainIDs`
- `IsSupportedChainID`
- `ChainName`
- `IsTRONChain`
- `NewChainFactory`
- `WalletRepo.Create`
- `WalletRepo.FindByChainAddress`
- `WalletRepo.EnsureAllAddresses`
- `WalletAddressForChainID`
- `AddressIndex.Load`

Eksik:
- Chain ekleme icin declarative config yok; code-level registration ve schema mapping gerekiyor.
- Wallet address modeli normalized `wallet_addresses` tablosunu ana kaynak olarak kullanmiyor; chain kolonlari hala merkezi.
- TRON mainnet/testnet adres ayrimi wallet schema'sinda yok.
- Chain-family policy'leri tek yerde degil.

Risk:
- Yeni chain eklemek migration, model, repo, lookup, listener, asset registry, route/admin UI gibi birden fazla noktada degisiklik gerektirir.
- Chain kolonlari arttikca wallet modeli buyur ve test yuku artar.
- Testnet/mainnet ayrimi kolon bazinda yeterince guclu degil.

Oneri:
- Normalized `wallet_addresses(wallet_id, chain_id, address, derivation_path, derivation_version)` modelini ana kaynak haline getirin.
- Chain registration, finality, derivation, fee, listener ve capability ayarlarini tek chain registry altinda toplayin.
- Testnet/mainnet izolasyonunu chain-id bazli address storage ve policy ile zorunlu hale getirin.
