# Gateway - Multi-Chain Crypto Payment Gateway and Wallet Provider Infrastructure

Dil: [English](README.md) | **Türkçe**

Gateway, Go ile geliştirilmiş bir **B2B crypto payment gateway**, **kripto ödeme geçidi** ve **wallet-provider altyapısıdır**. Merchant'ların kripto para ile ödeme almasını, geliştiricilerin kullanıcı bazlı çok zincirli cüzdan adresleri üretmesini ve wallet/exchange tarzı ürünlerin deposit, payment, payout, refund, sweep, webhook, ledger ve reconciliation akışlarını tek para çekirdeği üzerinden yönetmesini sağlar.

Proje sadece bir checkout sayfası değildir. Hosted checkout, static deposit address, merchant API, HMAC-signed webhooks, blockchain listeners, ledger-backed balances, finality gates, sweep operations, refund/payout workflows, provider health ve production readiness kontrollerini aynı Go uygulaması içinde birleştirir. Uygulama Go Fiber v3, GORM, PostgreSQL, Trust Wallet Core, Tailwind CSS ve server-rendered merchant/admin/checkout ekranları üzerine kuruludur.

Kapsadığı ana arama niyetleri: `crypto payment gateway`, `multi-chain payment gateway`, `cryptocurrency payment processor`, `wallet provider API`, `merchant crypto checkout`, `static deposit address`, `blockchain payment gateway`, `HMAC webhook payment API`, `Go payment gateway`, `PostgreSQL ledger reconciliation`.

## Bu Proje Ne Yapar?

Gateway, merchant ve wallet ekiplerinin kripto para ödeme altyapısını sıfırdan kurmak zorunda kalmadan kontrollü şekilde canlıya çıkabilmesi için tasarlanmıştır:

- Merchant'lar için hosted checkout, invoice/payment link, static deposit address ve `/api/v1` merchant API sağlar.
- Bitcoin, Ethereum, TRON, Solana, Avalanche, BNB Chain, Base, Arbitrum, Unichain ve Chiliz ağlarında adres üretimi, adres doğrulama, transfer/sweep hazırlığı ve listener altyapısını bir araya getirir.
- API key, API secret, HMAC imza, timestamp kontrolü, permission scope, IP allowlist ve idempotency ile entegrasyon sözleşmesini güçlendirir.
- On-chain transaction event'lerini durable chain fact, deposit processing, finality gate, ledger posting, payment matching ve money event outbox akışına bağlar.
- Merchant sistemlerine imzalı webhook event'leri gönderir; başarısız delivery kayıtlarını retry/replay ve operator diagnostics yüzeyleriyle yönetir.
- Admin ve merchant portallarıyla domain, product, wallet, payment, webhook, withdrawal, refund, sweep ve recovery operasyonlarını görünür kılar.
- Production readiness, provider health, migration discipline, signer policy, metrics ve observability kontrolleriyle para hareketi risklerini görünür hale getirir.

## Hangi Problemleri Çözer?

| Problem | Gateway'in Çözümü | Sonuç |
| --- | --- | --- |
| Merchant kripto ödeme almak istiyor ama her chain için ayrı checkout, wallet, listener ve webhook yazmak istemiyor. | Hosted checkout, payment session, static deposit address, merchant API ve multi-chain asset registry tek üründe birleşir. | Merchant entegrasyonu daha hızlı ve daha az hataya açık olur. |
| Kullanıcı bazlı deposit adreslerini manuel üretmek ve izlemek operasyonel risk yaratıyor. | Trust Wallet Core tabanlı HD wallet/address üretimi, address lookup, blockchain listener ve deposit lifecycle servisleri kullanılır. | Kullanıcı/adres eşleşmesi ve ödeme takibi sistematik hale gelir. |
| On-chain ödeme geldi mi, kaç confirmation aldı, eksik/fazla ödendi mi soruları belirsiz kalıyor. | Payment session, expected amount, asset/chain eşleşmesi, finality worker ve payment status event'leri kullanılır. | Payment status merchant, admin ve webhook tarafında izlenebilir olur. |
| Network retry'ları aynı order için birden fazla payment yaratabiliyor. | `Idempotency-Key` desteği request payload fingerprint'i ile birlikte çalışır. | Client retry güvenli hale gelir; duplicate invoice riski azalır. |
| Webhook callback'leri düşerse merchant sistemi para olaylarını kaçırabiliyor. | Durable webhook delivery kayıtları, HMAC signature, retry, replay ve diagnostics yüzeyleri bulunur. | Merchant callback akışı denetlenebilir ve tekrar oynatılabilir olur. |
| Bakiye ve para hareketi sadece transaction tablosundan okunursa reconciliation zorlaşır. | Ledger-derived balance yaklaşımı, reserve reconciliation job'ları ve money event outbox akışı kullanılır. | Bakiye otoritesi netleşir; drift ve recovery işleri görünür olur. |
| Withdrawal, refund ve sweep işlemleri private key, fee, nonce/UTXO ve operasyonel onay riski taşır. | Signer mode guard, chain resource policy, sweep job, refund/payout lifecycle ve readiness kontrolleriyle risk kapıları ayrılır. | Kontrollü beta için operasyonel sınırlar netleşir; production custody iddiası kanıta bağlanır. |
| "Production-ready payment gateway" iddiasının gerçek durumu belirsiz kalıyor. | Controlled launch readiness seviyeleri, `/api/v1/common/readiness`, `/metrics`, runbook ve audit dokümanları kullanılır. | Hangi aşamada beta, hangi aşamada production, hangi aşamada wallet-provider custody denebileceği açık kalır. |

## Kimler İçin?

- **Merchant ve dealer ekipleri:** Kripto ile ödeme almak, checkout linki vermek, statik deposit adresi üretmek ve webhook ile kendi sistemini güncellemek isteyen işletmeler.
- **Geliştirici entegratörler:** API key, HMAC signing, idempotency, hosted checkout, static wallet ve webhook contract'ı olan bir crypto payment API'ye bağlanmak isteyen ekipler.
- **Wallet ve exchange benzeri ürün ekipleri:** Kullanıcı bazlı multi-chain deposit wallet, ledger, reconciliation, withdrawal/refund/sweep ve provider health altyapısını tek çekirdekte toplamak isteyen platformlar.
- **Operasyon ve güvenlik ekipleri:** Para hareketi, webhook backlog, signer readiness, provider lag, migration state, reconciliation drift ve admin recovery işlerini görünür yönetmek isteyen ekipler.

## Temel Özellikler

- Multi-chain HD wallet/address generation ve address validation
- Hosted crypto checkout, invoice/payment link, QR code ve websocket payment status
- Static deposit address API ve kullanıcı bazlı wallet altyapısı
- Merchant portalı: domain, API credential, product, invoice, withdrawal ve webhook yönetimi
- Admin paneli: merchant, withdrawal, refund, sweep, webhook replay, recovery ve admin kullanıcı yönetimi
- `/api/v1` merchant API: API key, Bearer token, HMAC request signing, permission scopes ve IP allowlist
- Idempotent payment creation ve duplicate request koruması
- Blockchain listener worker'ları: Bitcoin, EVM family, Solana ve TRON event ingestion
- Durable chain fact, deposit fact processing, transaction finality ve payment matching
- Webhook delivery, retry, replay, diagnostics ve imzalı callback event'leri
- Exact EVM/Solana şemalarıyla statik HTTP kaynakları ve dinamik hosted checkout ödemeleri için opt-in x402 v2 seller middleware'i
- Ledger, withdrawal request, refund, sweep job, price quote, provider health ve activity log modelleri
- CoinGecko fiyat servisi entegrasyonu ve custom token price override desteği
- Prometheus uyumlu `/metrics`, request observability ve readiness endpoint'leri
- Swagger/OpenAPI çıktısı ve entegrasyon dokümantasyonu

## Desteklenen Ağlar ve Varlıklar

Zincir kayıtları `application/configuration/chains.go`, varlık kayıtları `application/configuration/assets.go` dosyasında tutulur.

Desteklenen ağlar:

- Bitcoin
- Ethereum
- TRON ve TRON Nile testnet
- Solana
- Avalanche C-Chain
- BNB Chain
- Chiliz ve Chiliz Spicy testnet
- Base
- Arbitrum One
- Unichain

Örnek kayıtlı varlıklar:

- Native varlıklar: BTC, ETH, TRX, SOL, AVAX, BNB, CHZ
- Stablecoin ve wrapped tokenlar: USDT, USDC, WBTC, WETH, WBNB, WAVAX, WCHZ, WSOL
- ERC-20, SPL ve TRC-20 token kayıtları
- Proje özelinde kayıtlı tokenlar: TBT, CHZINU, PEPPER, COOLVIBES
- Wrapped token alias'ları: WBTC -> BTC, WETH -> ETH, WBNB -> BNB, WAVAX -> AVAX, WCHZ -> CHZ, WSOL -> SOL

## Çalışma Akışı

1. Merchant portalı veya admin/internal endpoint'ler üzerinden merchant hesabı oluşturulur.
2. Merchant için domain kaydı, API key, API secret, webhook URL, webhook secret ve izin kapsamları ayarlanır.
3. Merchant `/api/v1/payment/create`, hosted checkout veya panel üzerinden invoice/payment session oluşturur.
4. Kullanıcı checkout ekranında desteklenen ağ ve varlığı seçer.
5. Gateway ödeme oturumu için deposit address, expected amount, QR code ve realtime status kanalı üretir.
6. Blockchain listener worker'ları ilgili ağları izler ve on-chain event'leri durable chain fact olarak kaydeder.
7. Deposit fact worker address ownership, chain, asset, amount ve finality bilgilerini eşleştirir.
8. Payment session beklenen tutar ve asset ile eşleştiğinde payment status güncellenir; underpaid, overpaid, partial paid veya succeeded event'leri üretilebilir.
9. Ledger entry, deposit lifecycle, sweep/refund/payout job ve money event outbox akışları idempotent şekilde ilerler.
10. Webhook notifier merchant callback adresine imzalı event gönderir; başarısız denemeler retry/replay kuyruğunda yönetilir.

## Product Readiness

Gateway şu an kontrollü merchant/dealer beta için değerlendirilecek şekilde konumlandırılmıştır. Production payment gateway, wallet-provider custody ve exchange-grade tracking iddiaları ayrı readiness gate'lerine bağlıdır. Özellikle production custody için process içi software signer kullanılmamalı; KMS/HSM/MPC/Vault veya eşdeğer external signer adapter'ı, reconciliation kanıtı, compliance kapsamı ve operasyonel runbook'lar tamamlanmadan yüksek hacimli müşteri fonu tutulmamalıdır.

Detaylı durum ve sınırlar:

- [Controlled Launch Readiness](docs/controlled-launch-readiness.md)
- [Product Readiness Audit](docs/product-readiness-audit.md)
- [Payment Gateway Wallet Provider Audit](docs/payment-gateway-wallet-provider-audit.md)
- [Integration Guide](docs/integration-guide.md)
- [Money Path Observability Runbook](docs/money-path-observability-runbook.md)
- [Sweep Operations Runbook](docs/sweep-operations-runbook.md)

## Geliştirici Dokümantasyonu

Geliştirici odaklı katkı ve genişletme notları [Geliştirici Rehberi](docs/developer-guide.tr.md) içinde yer alır. Rehber lokal geliştirme döngüsünü, kod sahipliği sınırlarını, sık değişiklik checklist'lerini, migration'ları, worker'ları, API değişikliklerini, asset/chain eklemeyi, testleri ve PR beklentilerini açıklar.

## Proje Yapısı

```text
.
├── api/
│   ├── handlers/          # HTTP handler'ları, checkout, merchant portal, admin ve v1 API
│   ├── middleware/        # Security header, observability, CORS ve rate limit middleware'leri
│   ├── router/            # Action router yardımcıları
│   └── routes/            # Fiber route kayıtları ve Swagger route'ları
├── application/
│   └── configuration/     # Chain factory ve asset registry konfigürasyonu
├── asset/                 # Varlık tipleri, deployment kayıtları ve registry
├── blockchain/            # Chain interface, factory, walletcore provider ve zincir implementasyonları
├── constants/             # Komut, ürün, webhook event ve chain sabitleri
├── contracts/             # ERC20 ve Multicall sözleşme bağlayıcıları
├── docs/                  # Entegrasyon, readiness, runbook, audit ve Swagger/OpenAPI çıktıları
├── helpers/               # Şifreleme, credential ve yardımcı fonksiyonlar
├── models/                # GORM modelleri
├── repositories/          # Veritabanı erişim katmanı
├── services/              # Deposit, pricing, realtime, reconciliation, tx rescan, system ve webhook servisleri
├── static/                # Chain/coin görselleri
├── types/                 # Request/response DTO'ları
├── views/                 # Merchant portal, admin, gateway ve invoice HTML view'ları
└── workers/               # Listener, dispatcher, supervisor ve address index worker'ları
```

## Gereksinimler

- Go `1.25.4`
- PostgreSQL
- Node.js ve npm (Tailwind CSS çıktısını üretmek için)
- Git submodule desteği ve Trust Wallet Core kaynak kodu
- Trust Wallet Core native library build'i (CGo, clang/cmake ve `scripts/build_wallet_core.sh`)
- Chain listener'ların sağlıklı çalışması için ilgili ağlara RPC erişimi

## Kurulum

Repoyu Trust Wallet Core submodule'üyle birlikte çekin:

```bash
git clone --recurse-submodules <repo-url> gateway
cd gateway
```

Repo daha önce normal `git clone` ile çekildiyse veya submodule klasörü boş geldiyse şu komutu çalıştırın:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

Mevcut clone içinde submodule URL bilgisini yenilemek gerekirse:

```bash
git submodule sync --recursive
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

Submodule hazır olduğunda şu dosya mevcut olmalıdır:

```bash
test -f third_party/trustwallet/wallet-core/samples/go/go.mod
```

`go.mod` içinde `replace tw => ./third_party/trustwallet/wallet-core/samples/go` bulunduğu için submodule başlatılmadan `go mod download`, `go test` veya `go run .` çalıştırıldığında şu hata alınır:

```text
tw@v0.0.0: replacement directory ./third_party/trustwallet/wallet-core/samples/go does not exist
```

Bu durumda çözüm fallback kullanmak değil, submodule'ü başlatmaktır:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

Trust Wallet Core bu projede zorunludur. Mnemonic doğrulama, HD private key türetme, zincir adresi üretimi ve para hareketi imzalama yolları Trust Wallet Core üzerinden yürür. `walletcorefallback` build tag'i sadece dar kapsamlı lokal debug içindir; ödeme, withdrawal, refund, sweep veya production çalıştırma için kullanılmamalıdır.

Bağımlılıkları kurun ve native Trust Wallet Core library dosyalarını üretin:

```bash
go mod download
npm install
./scripts/build_wallet_core.sh
npm run build:css
```

`./scripts/build_wallet_core.sh` macOS'ta `tools/install-sys-dependencies-mac`, Linux'ta `tools/install-sys-dependencies-linux` çalıştırır; ardından Trust Wallet dependency, Rust dependency, native generated file ve CMake build adımlarını yürütür. Build tamamlanmadan wallet üretimi veya transfer signing beklenmemelidir.

`.env` dosyasını hazırlayın:

```bash
cp .env.sample .env
```

Lokal minimum `.env` örneği:

```env
DATABASE_URL="host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable"
PORT=":3001"
APP_ENV=development
GATEWAY_LOG_FORMAT=text
GATEWAY_LOG_LEVEL=info
HTTP_READ_TIMEOUT=15s
HTTP_WRITE_TIMEOUT=30s
HTTP_IDLE_TIMEOUT=60s
CORS_ALLOWED_ORIGINS=http://localhost:3001
ALLOW_PRIVATE_WEBHOOK_URLS=true
ALLOW_AUTOMIGRATE_IN_PRODUCTION=false
SIGNER_MODE=software
ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=false
METRICS_BEARER_TOKEN=
PROVIDER_HEALTH_INTERVAL=1m
PROVIDER_HEALTH_TIMEOUT=8s
PROVIDER_HEALTH_STALE_LAG_BLOCKS=3
PROVIDER_FAILOVER_STRATEGY=prefer_healthy
MASTER_KEY=32-byte-or-longer-secret
PORTAL_JWT_SECRET=32-byte-or-longer-portal-jwt-secret
MNEMONIC_PHRASE="your bip39 mnemonic phrase"
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=change-this-password
ADMIN_NAME=Gateway Admin
TRON_GRPC_ENDPOINTS=grpc.trongrid.io:50051
TRON_TESTNET_JSONRPC_URLS=https://nile.trongrid.io/jsonrpc
TRON_TESTNET_HTTP_ENDPOINTS=https://nile.trongrid.io
TRON_TESTNET_GRPC_ENDPOINTS=grpc.nile.trongrid.io:50051
TRON_TESTNET_SWEEP_ADDRESS=
```

Veritabanını migrate edin:

```bash
go run . -migrate
```

Seed datalarını eklemek için:

```bash
go run . -seed
```

Migration ve seed işlemini birlikte çalıştırmak için:

```bash
go run . -install
```

Uygulamayı başlatın:

```bash
go run .
```

Geliştirme sırasında Go ve HTML değişikliklerini otomatik almak için Air kullanın:

```bash
go install github.com/air-verse/air@latest
npm run dev
```

Air uygulamayı `.env` içindeki `PORT=:3001` üzerinde çalıştırır; tarayıcı auto-refresh için sayfayı Air proxy'den açın: `http://localhost:3000`. `APP_ENV=development` altında Fiber HTML şablonlarını her render'da yeniden yükler ve statik asset cache'i kapatır. `PORT` değerini değiştirirseniz `.air.toml` içindeki `proxy.app_port` değerini de aynı porta çekin. Tailwind kaynak CSS'i değiştiriyorsanız ikinci terminalde watcher çalıştırın:

```bash
npm run dev:css
```

Geçici port override örneği:

```bash
PORT=:3001 go run .
```

`go run .` uygulama başlangıcında `.env` dosyasını yükler. Shell içinde daha önce set edilmiş environment değerleri `.env` değerlerinin önüne geçer. `PORT` değeri Fiber formatında verilmelidir; örnek: `:3001`.

## Ortam Değişkenleri

Parametre formatları:

| Format | Açıklama |
| --- | --- |
| Duration | Go duration formatı kullanılır. Örnek: `30s`, `5m`, `1h`. |
| RPC listesi | Virgülle ayrılmış URL listesi kullanılır. Örnek: `https://rpc-1,https://rpc-2`. |
| Amount/raw unit | Gas, fee, prefund ve transfer policy değerleri chain'in en küçük birimindedir. Örnek: wei, sun, lamports, sat/vbyte. |
| Chain name | Env isimlerinde chain adı uppercase ve tire yerine `_` ile yazılır. Örnek: `CHILIZ_SPICY_RPC_URLS`. |

Zorunlu veya kritik değişkenler:

| Değişken | Açıklama |
| --- | --- |
| `DATABASE_URL` | PostgreSQL bağlantı adresi. URL veya `key=value` DSN formatı kullanılabilir. Uygulama bu değer olmadan başlamaz. |
| `PORT` | Fiber listen adresi. Örnek: `:3001`. |
| `APP_ENV` | Çalışma ortamı. `production` değerinde bazı güvenlik kontrolleri sıkılaşır. Lokal geliştirme için `development` kullanılabilir. |
| `ALLOW_PRIVATE_WEBHOOK_URLS` | Lokal geliştirmede `localhost`, `127.0.0.1` veya özel ağ IP'lerine webhook göndermeye izin verir. `APP_ENV=production` iken dikkate alınmaz. |
| `ALLOW_AUTOMIGRATE_IN_PRODUCTION` | Varsayılan `false`. `APP_ENV=production` iken startup `AutoMigrate` çalışmaz; schema harici/versioned migration süreciyle yönetilmelidir. Sadece kontrollü bakım penceresinde geçici `true` yapılmalıdır. |
| `GATEWAY_DB_MIGRATION_VERSION` | Production readiness için uygulanmış son versioned GORM migration artifact id'si. `APP_ENV=production` altında `services/dbmigrations.LatestID()` ile eşleşmelidir. |
| `ADDRESS_INDEX_PRELOAD_LIMIT` | Chain listener'ların ihtiyaç duyduğu eksiksiz ve otoritatif cüzdan-adres index'ini yüklemek için unset bırakın (varsayılan `-1`). `0` dahil herhangi bir sonlu limit index'i non-authoritative tutar; listener'lar event başına DB sorgusu yapmak veya hatalı ownership eşleşmesi riski almak yerine fail-closed kalır. |
| `SIGNER_MODE` | `software`, `kms`, `hsm`, `mpc`, `vault` veya external custody modu. `software` sadece development içindir; production signing ve watch-only address derivation aktif external custody adapter olmadan hard-fail eder. |
| `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION` | Legacy risk marker. `true` olsa bile `APP_ENV=production` altında software signing'e izin vermez ve production readiness'ı geçirmez. |
| `METRICS_BEARER_TOKEN` | `/metrics` Prometheus endpoint'i için bearer token. `APP_ENV=production` iken zorunludur; boş bırakılırsa endpoint 503 döner. |
| `PROVIDER_HEALTH_INTERVAL` | Provider health worker periyodu. Varsayılan `1m`. |
| `PROVIDER_HEALTH_TIMEOUT` | Tek provider probe timeout değeri. Varsayılan `8s`. |
| `PROVIDER_HEALTH_STALE_LAG_BLOCKS` | Aynı chain içindeki referans head'e göre stale/degraded sayılacak blok/slot farkı. Varsayılan `3`. |
| `PROVIDER_FAILOVER_STRATEGY` | `observe` varsayılanı sadece raporlar. `prefer_healthy` sağlıklı provider'ları `RPCs()` sıralamasında öne alır. `fail_closed` ileride production degraded-mode politikaları için explicit strateji değeridir. |
| `PROVIDER_HEALTH_REQUIRE_HASH` | `true` ise hash kanıtı olmayan provider snapshot'ları degraded işaretlenir. Bazı chain ailelerinde hash kanıtı bu hikayede unavailable kalabilir. |
| `PORTAL_JWT_SECRET` | Merchant/admin portal mutasyon JWT imzalama secret'ı. Production'da stabil olmalıdır. Yoksa `DEALER_SESSION_SECRET`, `SESSION_SECRET` veya `MASTER_KEY` fallback kullanılır. |
| `DEALER_SESSION_SECRET` / `SESSION_SECRET` | Merchant/admin session imzalama fallback secret'ları. Production'da rastgele runtime secret'a düşülmemelidir. |
| `MASTER_KEY` | API secret, webhook secret ve credential şifreleme işlemlerinde kullanılır. |
| `MNEMONIC_PHRASE` | Trust Wallet Core ile BIP39 doğrulama, HD wallet üretimi ve development signing için kullanılan mnemonic. `APP_ENV=production` altında uygulama process'i mnemonic/private-key helper'larını kullanmaz; production custody external adapter sınırında kalmalıdır. |
| `ADMIN_EMAIL` | Bootstrap admin hesabı için e-posta. |
| `ADMIN_PASSWORD` | Bootstrap admin hesabı için parola. |
| `ADMIN_NAME` | Bootstrap admin görünen adı. |

Opsiyonel servis değişkenleri:

| Değişken | Varsayılan / Açıklama |
| --- | --- |
| `GATEWAY_LOG_FORMAT` | `json` veya `text`. Boş bırakılırsa production'da `json`, diğer ortamlarda `text` kullanılır. |
| `GATEWAY_LOG_LEVEL` | `debug`, `info`, `warn` veya `error`. Varsayılan: `info`. |
| `HTTP_READ_TIMEOUT` | HTTP request okuma timeout'u. Varsayılan: `15s`. |
| `HTTP_WRITE_TIMEOUT` | HTTP response yazma timeout'u. Varsayılan: `30s`. |
| `HTTP_IDLE_TIMEOUT` | Keep-alive idle timeout'u. Varsayılan: `60s`. |
| `CORS_ALLOWED_ORIGINS` | Virgülle ayrılmış izinli origin listesi. Boş origin isteklerine izin verilir. |
| `API_KEY_RATE_LIMIT_PER_MINUTE` | `/api/v1` için dakika başına limit. Varsayılan: `120`. |
| `X402_ENABLED` | Genel statik x402 seller route'larını açar. Payment-link x402 merchant panel/API üzerinden link bazında ayarlanır. Varsayılan: `false`. |
| `X402_FACILITATOR_URL` | x402 facilitator adresi. Varsayılan: `https://x402.org/facilitator` (testnet facilitator). |
| `X402_NETWORKS` / `X402_NETWORK` | x402 tarafından kabul edilecek virgülle ayrılmış CAIP-2 network listesi. Varsayılan: `eip155:84532` (Base Sepolia). Bu entegrasyon EVM (`eip155:*`) ve Solana (`solana:*`) exact payment destekler. |
| `X402_PAY_TO` | Genel statik x402 route'ları için sabit alıcı adresi. Network özel override için `X402_PAY_TO_EIP155_84532` veya `X402_PAY_TO_SOLANA_<reference>` kullanılabilir. Payment-link modunda ayarlanmaz. |
| `X402_PRICE` | Genel statik route'lar için exact-scheme sabit fiyatı; örnek: `$0.001`. |
| `X402_ROUTES` | Genel statik route pattern listesi; virgül, noktalı virgül veya satır sonuyla ayrılabilir. Örnek: `GET /your-paid-route`. Yalnızca uygulamada kayıtlı route'ları yazın. |
| `X402_ROUTE_DESCRIPTION` / `X402_SERVICE_NAME` | x402 payment requirement içine eklenen opsiyonel metadata. |
| `X402_SYNC_FACILITATOR_ON_START` | Startup sırasında facilitator'ın desteklediği scheme listesini senkronize eder. Varsayılan: `true`; facilitator'ın sağladığı Solana fee-payer metadata'sı için gereklidir. |
| `X402_TIMEOUT` | Facilitator verify ve settlement işlemleri için timeout. Varsayılan: `30s`. |
| `WEBHOOK_RETRY_INTERVAL` | Webhook retry worker aralığı. Varsayılan: `30s`. |
| `WEBHOOK_MAX_ATTEMPTS` | Webhook delivery maksimum deneme sayısı. |
| `WEBHOOK_RETRY_BACKOFF_BASE` | Webhook retry exponential backoff başlangıç süresi. |
| `WEBHOOK_RETRY_BACKOFF_MAX` | Webhook retry exponential backoff üst sınırı. |
| `WEBHOOK_DELIVERY_CLAIM_TIMEOUT` | Claimed webhook delivery kilit timeout'u. |
| `TRANSACTION_FINALITY_INTERVAL` | Pending transaction finality worker aralığı. Varsayılan: `20s`. |
| `DEPOSIT_FACT_INTERVAL` | Deposit fact processing worker aralığı. Varsayılan: `10s`. |
| `SWEEP_JOB_INTERVAL` | Sweep worker aralığı. Varsayılan: `15s`. |
| `SWEEP_JOB_LOCK_TIMEOUT` | Sweep job lock timeout'u. Minimum execution timeout altında verilirse güvenli minimuma çekilir. |
| `SWEEP_PREFUND_RETRY_AFTER` | Sweep prefund başarısızlığı sonrası retry bekleme süresi. Varsayılan: `10m`. |
| `RECONCILIATION_INTERVAL` | Ledger/reserve reconciliation worker aralığı. Varsayılan: `5m`. |
| `RESERVE_RECONCILIATION_LIMIT` | Reserve reconciliation batch limiti. Varsayılan: `200`, üst sınır: `1000`. |
| `GATEWAY_SHUTDOWN_TIMEOUT` | Graceful shutdown timeout'u. Varsayılan: `5s`. |
| `GATEWAY_VERBOSE_TX` | Transaction log detayını açar. `true`, `1`, `yes`, `on` veya `verbose` kabul edilir. |
| `GATEWAY_VERBOSE_EVENTS` | Chain event log detayını açar. `true`, `1`, `yes`, `on` veya `verbose` kabul edilir. |
| `FINALITY_CONFIRMATIONS_DEFAULT` | Genel confirmation fallback değeri. |
| `CHAIN_<id>_CONFIRMATIONS` | Chain ID bazlı confirmation override. |
| `<CHAIN_NAME>_CONFIRMATIONS` | Chain slug bazlı confirmation override. Örnek: `ETHEREUM_CONFIRMATIONS`. |
| `CHAIN_<id>_START_BLOCK` | Listener başlangıç block/slot override'u. |
| `<CHAIN_NAME>_START_BLOCK` | Chain slug bazlı listener başlangıç override'u. Örnek: `ETHEREUM_START_BLOCK`. |
| `START_BLOCK_<CHAIN_NAME>` | Alternatif listener başlangıç override'u. |
| `CHAIN_START_BLOCK_DEFAULT` | Tüm chain'ler için default başlangıç block/slot değeri. |
| `COINGECKO_BASE_URL` | CoinGecko API adresi. Varsayılan: `https://api.coingecko.com/api/v3`. |
| `COINGECKO_CACHE_TTL` | Fiyat cache süresi. |
| `COINGECKO_RATE_LIMIT_COOLDOWN` | CoinGecko rate limit sonrası bekleme süresi. |
| `COINGECKO_API_KEY` | CoinGecko API anahtarı. |
| `PRICE_<SYMBOL>_<CURRENCY>` | CoinGecko'da olmayan özel token fiyatı. Örnek: `PRICE_PEPPER_USD=0.0001`. |
| `OIDC_AUTHORITY` / `OIDC_ISSUER_URL` | OIDC discovery authority/issuer adresi. |
| `OIDC_AUTH_URL` | OIDC authorization endpoint override'u. |
| `OIDC_TOKEN_URL` | OIDC token endpoint override'u. |
| `OIDC_USERINFO_URL` | OIDC userinfo endpoint override'u. |
| `OIDC_CLIENT_ID` | Merchant portal OIDC login için client ID. |
| `OIDC_CLIENT_SECRET` | Merchant portal OIDC login için client secret. |
| `OIDC_REDIRECT_URI` | OIDC callback adresi. |
| `OIDC_PROVIDER_NAME` | OIDC provider adı. |
| `OIDC_SCOPES` | OIDC scope listesi. |
| `OIDC_PROMPT` | OIDC prompt parametresi. |

RPC değişkenleri:

| Format | Açıklama |
| --- | --- |
| `<CHAIN_NAME>_RPC_URLS` | Virgülle ayrılmış RPC listesi. Örnek: `ETHEREUM_RPC_URLS`. |
| `CHAIN_<id>_RPC_URLS` | Chain ID bazlı RPC listesi. Örnek: `CHAIN_1_RPC_URLS`. |
| `BSC_RPC_URLS`, `BINANCE_RPC_URLS` | BNB Chain alias RPC değişkenleri. |
| `BITCOIN_RPC_URLS` / `CHAIN_0_RPC_URLS` | Bitcoin endpoint listesi. Listener bu chain RPC URL'lerinden UniSat Open API, Bitcoin Core JSON-RPC ve Esplora uyumlu API tiplerini otomatik ayırır. |
| `TRON_JSONRPC_URLS` | TRON JSON-RPC endpoint listesi. |
| `TRON_HTTP_ENDPOINT` / `TRON_HTTP_ENDPOINTS` | TRON HTTP API endpoint ayarları. |
| `TRON_GRPC_ENDPOINT` / `TRON_GRPC_ENDPOINTS` | TRON listener gRPC endpoint ayarları. |
| `TRON_TESTNET_JSONRPC_URLS` | TRON Nile testnet JSON-RPC endpoint listesi. Shasta veya başka bir TRON testnet için override edilebilir. |
| `TRON_TESTNET_HTTP_ENDPOINT` / `TRON_TESTNET_HTTP_ENDPOINTS` | TRON Nile testnet HTTP API endpoint ayarları. Shasta veya başka bir TRON testnet için override edilebilir. |
| `TRON_TESTNET_GRPC_ENDPOINT` / `TRON_TESTNET_GRPC_ENDPOINTS` | TRON Nile testnet listener gRPC endpoint ayarları. Shasta veya başka bir TRON testnet için override edilebilir. |
| `TRON_PRO_API_KEY` | TRON API erişimi için opsiyonel anahtar. |
| `REQUIRE_EVM_TRACE` | `true` ise EVM listener trace bağımlılığını zorunlu kılar. |
| `DEBUG_EVM_TRACE` | `true` ise EVM trace debug loglarını açar. |

Gas, fee, sweep ve prefund ayarları:

| Değişken | Açıklama |
| --- | --- |
| `EVM_GAS_THRESHOLD_WEI` | EVM cüzdan gas eşiği. |
| `EVM_GAS_PREFUND_WEI` | EVM prefund miktarı. |
| `EVM_MAX_GAS_PRICE_WEI` | EVM gas price policy üst sınırı. |
| `<CHAIN_NAME>_SWEEP_ADDRESS` | Chain bazlı sweep hedef adresi. Örnek: `ETHEREUM_SWEEP_ADDRESS`. |
| `EVM_SWEEP_ADDRESS` | Tüm EVM chain'ler için fallback sweep adresi. |
| `SWEEP_ADDRESS` | Bitcoin, EVM, Solana ve TRON için genel sweep fallback adresi. |
| `BITCOIN_SWEEP_ADDRESS` / `BTC_SWEEP_ADDRESS` | Bitcoin sweep hedef adresi. |
| `BITCOIN_MIN_FEE_RATE_SAT_PER_VBYTE` | Bitcoin fee rate alt sınırı. Varsayılan: `1`. |
| `BITCOIN_MAX_FEE_RATE_SAT_PER_VBYTE` | Bitcoin fee rate üst sınırı. Varsayılan: `10000`. |
| `BITCOIN_FEE_RATE_SAT_PER_VBYTE` | Bitcoin transaction fee rate değeri. Varsayılan: `10`. |
| `TRON_GAS_THRESHOLD_SUN` | TRON gas eşiği. |
| `TRON_GAS_PREFUND_SUN` | TRON prefund miktarı. |
| `TRON_TRC20_FEE_LIMIT_SUN` | TRC-20 transfer fee limit'i. Varsayılan: `50000000`. |
| `TRON_NATIVE_SWEEP_FEE_SUN` | TRON native sweep fee rezervi. Varsayılan: `1100000`. |
| `TRON_SWEEP_ADDRESS` / `TRX_SWEEP_ADDRESS` | TRON sweep hedef adresi. |
| `TRON_TESTNET_SWEEP_ADDRESS` / `TRX_TESTNET_SWEEP_ADDRESS` / `NILE_SWEEP_ADDRESS` / `TRON_NILE_SWEEP_ADDRESS` / `SHASTA_SWEEP_ADDRESS` | TRON testnet sweep hedef adresi. |
| `SOLANA_GAS_THRESHOLD_LAMPORTS` | Solana gas eşiği. |
| `SOLANA_GAS_PREFUND_LAMPORTS` | Solana prefund miktarı. |
| `SOLANA_TRANSFER_FEE_LAMPORTS` | Solana transfer fee değeri. Varsayılan: `5000`. |
| `SOLANA_SWEEP_ADDRESS` | Solana sweep hedef adresi. |

## HTTP Arayüzleri

Ana web ekranları:

- `/` - Merchant portal ana sayfası
- `/merchant/login` - Merchant portal login
- `/merchant/register` - Merchant kayıt
- `/merchant/dashboard` - Merchant dashboard
- `/merchant/onboarding` - Onboarding ekranı
- `/admin/login` - Admin login
- `/admin` - Admin dashboard
- `/payment-links/:token` - Payment link
- `/checkout/:token` - Checkout ekranı
- `/checkout/:token/pay` - Ödeme ekranı
- `/checkout/:token/ws` - Checkout websocket status kanalı
- `/checkout/:token/qr.png` - Ödeme QR kodu
- `/checkout/:token/status.json` - Checkout durum JSON'u
- `/invoice/:token` - Invoice ekranı

## x402 HTTP Ödemeleri

Gateway, mevcut Fiber route'ları ve hosted checkout akışı için opt-in x402 v2 seller olarak çalışabilir. x402 varsayılan olarak kapalıdır ve merchant API key, HMAC veya portal authentication akışlarının yerine geçmez.

Entegrasyon, resmi [`x402-foundation/x402` Go SDK'sını](https://github.com/x402-foundation/x402/tree/main/go) Fiber-to-`net/http` adaptörü üzerinden kullanır. EVM ve Solana network'lerinde `exact` şeması desteklenir:

Protokol referansları: [`HTTP 402 core concepts`](https://docs.x402.org/core-concepts/http-402) ve [`seller quickstart`](https://docs.x402.org/getting-started/quickstart-for-sellers).

1. Ödemesiz istek `402 Payment Required` ve Base64 kodlu `PAYMENT-REQUIRED` header'ı ile yanıtlanır.
2. Buyer ödemeyi imzalar ve `PAYMENT-SIGNATURE` ile isteği tekrar gönderir.
3. Facilitator ödemeyi doğrular ve settlement yapar.
4. Başarılı response, settlement bilgisini `PAYMENT-RESPONSE` header'ında taşır.

İki seller modu vardır:

- Genel statik kaynaklar: yalnızca `X402_ROUTES` içindeki route'lar korunur; `X402_PAY_TO` ve `X402_PRICE` deployment seviyesinde sabittir.
- Payment link'ler: merchant panelinde link oluştururken x402 açılır veya payment-link API'sine `x402_enabled` gönderilir. Network ve token, checkout formunda seçilen asset'ten alınır; middleware checkout token'ıyla `PaymentSession` yükler ve dinamik olarak `DepositAddress` değerini `payTo`, `SelectedToken` değerini asset, `ExpectedAmountRaw` değerini amount olarak kullanır. `X402_PAY_TO`, `X402_PRICE` ve checkout route env ayarları kullanılmaz.

Aktivasyon için deployment ayarları (Base Sepolia değerleri test amaçlıdır):

```env
X402_ENABLED=true
X402_FACILITATOR_URL=https://x402.org/facilitator
X402_NETWORKS=eip155:84532
X402_PAY_TO=0x1111111111111111111111111111111111111111
X402_PRICE=$0.001
X402_ROUTES=GET /payment-links/*
X402_SYNC_FACILITATOR_ON_START=true
X402_TIMEOUT=30s
```

Merchant payment link için link oluşturma formunda x402'yi açın (yalnızca sabit tutarlı link'lerde):

```json
{
  "x402_enabled": true
}
```

Network, payment link üzerindeki ayrı bir ayar değildir; checkout formunda seçilen token asset'in chain'inden türetilir. x402 exact checkout için seçilen asset'in token olması gerekir (ör. USDC); native ETH/SOL/BTC seçimleri ve donation link'ler normal checkout ödeme akışında devam eder.

`X402_ROUTES`, gateway tarafından gerçekten kayıt edilmiş genel route'ları göstermelidir. Birden fazla genel route virgül, noktalı virgül veya satır sonuyla ayrılabilir. Payment-link checkout route'u otomatik mount edilir ve linkteki x402 ayarıyla kontrol edilir. Genel çoklu-network kaynaklarda `X402_NETWORKS` CAIP-2 formatında yazılmalı; alıcılar farklıysa `X402_PAY_TO_SOLANA_<genesis-hash>` gibi network özel adres kullanılmalıdır.

Middleware varsayılan olarak startup sırasında facilitator'ın desteklediği scheme listesini senkronize eder; facilitator'ın sağladığı Solana fee-payer metadata'sı için bu gereklidir. Startup'ın facilitator'a bağlanmaması gerekiyorsa ve kullanılan network'ler bu metadata'ya ihtiyaç duymuyorsa `X402_SYNC_FACILITATOR_ON_START=false` yapılabilir. Request-time verify ve settlement yine ayarlı facilitator üzerinden yürür. x402 açıkken konfigürasyon geçersizse middleware eklenmez ve hata loglanır. Genel x402 settlement'ları doğrudan resource-server settlement'ıdır; checkout modundaki transferler session'ın üretilmiş deposit adresine gider ve mevcut chain listener/reconciliation akışı tarafından ilgili `PaymentSession` ile eşleştirilir.

Legacy/internal command endpoint örnekleri:

- `POST merchant.create`
- `POST merchant.fetch`
- `POST merchant.domain.create`
- `POST merchant.wallet.create`
- `POST system.withdraw`
- `POST system.sweep`
- `POST /payments/create`

## V1 Merchant API

Detaylı entegrasyon rehberi: [`docs/integration-guide.md`](docs/integration-guide.md)

`/api/v1` okuma endpoint'leri API key veya Bearer token ile kullanılır:

```http
X-API-Key: <domain-api-key>
```

veya:

```http
Authorization: Bearer <domain-api-key>
```

Para hareketi veya işlem oluşturan POST endpoint'leri ayrıca HMAC imzası ister:

```http
X-API-Key: <domain-api-key>
X-API-Secret: <domain-api-secret>
X-Gateway-Timestamp: <unix_seconds>
X-Gateway-Signature: sha256=<hmac_sha256(method + path/query + timestamp + raw_body)>
```

Common endpoint'ler:

- `GET /api/v1/common/status`
- `GET /api/v1/common/readiness`
- `GET /api/v1/common/balance`
- `GET /api/v1/common/prices`
- `GET /api/v1/common/currencies`
- `GET /api/v1/common/fiat-currencies`
- `GET /api/v1/common/networks`

Wallet provider endpoint'leri:

- `POST /api/v1/wallet/create`
- `GET /api/v1/wallet/info`
- `GET /api/v1/wallet/addresses`
- `GET /api/v1/wallet/list`
- `GET /api/v1/wallet/balance`
- `GET /api/v1/wallets`

Payment endpoint'leri:

- `POST /api/v1/payment/create`
- `POST /api/v1/payment/white-label`
- `POST /api/v1/payment/static-address`
- `GET /api/v1/payment/static-addresses`
- `GET /api/v1/payment/info`
- `GET /api/v1/payment/history`
- `GET /api/v1/payment/statistics`
- `GET /api/v1/payment/currencies`
- `GET /api/v1/payment/status-table`

Payout endpoint'leri:

- `POST /api/v1/payout/create`
- `GET /api/v1/payout/info`
- `GET /api/v1/payout/history`
- `GET /api/v1/payout/status-table`

Refund endpoint'leri:

- `POST /api/v1/refund/create`
- `GET /api/v1/refund/info`
- `GET /api/v1/refund/history`

Örnek ödeme oluşturma isteği:

```bash
curl -X POST http://localhost:3001/api/v1/payment/create \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -H "Idempotency-Key: order-2024-001" \
  -d '{
    "order_id": "ORD-2024-001",
    "amount": "25.00",
    "currency": "USD",
    "description": "Product purchase",
    "user_id": "customer_42",
    "success_url": "https://example.com/success",
    "cancel_url": "https://example.com/cancel"
  }'
```

Örnek statik adres oluşturma isteği:

```bash
curl -X POST http://localhost:3001/api/v1/payment/static-address \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -d '{
    "user_id": "customer_42",
    "chain_id": 1,
    "symbol": "USDT",
    "label": "Main wallet"
}'
```

Örnek wallet provider cüzdanı oluşturma isteği:

```bash
curl -X POST http://localhost:3001/api/v1/wallet/create \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -d '{
    "user_id": "customer_42",
    "product_id": "wallet"
  }'
```

## Swagger

Swagger UI:

- `/swagger/*`
- `/docs/*`

Swagger çıktısını yeniden üretmek için:

```bash
swag init -g main.go -o docs
```

## Veritabanı

Development ortamında migration işlemi `services/database/database.go` içindeki `AutoMigrate` ile aşağıdaki ana tabloları yönetir:

- `chain_states`
- `blocks`
- `chain_facts`
- `deposits`
- `domains`
- `merchants`
- `transactions`
- `ledger_entries`
- `wallets`
- `products`
- `payment_sessions`
- `idempotency_keys`
- `money_event_outboxes`
- `webhook_deliveries`
- `sweep_jobs`
- `withdrawal_requests`
- `refunds`
- `price_quotes`
- `reconciliation_jobs`
- `activity_logs`
- `provider_health_snapshots`
- `wallet_address_lookups`
- `admins`

Migration başında PostgreSQL `uuid-ossp` extension'ı etkinleştirilir. `APP_ENV=production` iken startup `AutoMigrate` varsayılan olarak kapalıdır; uygulama yalnızca beklenen schema kolonlarını doğrular. Production schema değişiklikleri `services/dbmigrations` içindeki versioned GORM artifact registry ile, bakım penceresinde ve startup path'inden ayrı uygulanmalıdır. Ayrıntı: `docs/production-migration-discipline.md`.

## Worker'lar

Uygulama başlarken şu arka plan süreçlerini başlatır:

- Address index yükleme ve eksik adres backfill işlemi
- Bootstrap admin hesabı oluşturma
- Webhook retry worker
- Payment session expiry worker
- Deposit fact processing worker
- Pending transaction finality worker
- Ledger/reserve reconciliation worker
- Bitcoin, Ethereum/EVM, Solana ve TRON listener worker'ları

Listener'lar aday transaction event'lerini dispatcher üzerinden publish eder. Durable `chain_facts` yazimindan once subscriber; asset'in kayitli oldugunu ve transferin confirmed, pozitif, eksiksiz platform wallet index'indeki bir `To` adresine geldigini dogrular; ayni merchant'in custody adresleri arasindaki hareketleri ve eksik token kimliklerini atlar. Bitcoin'de tum input adresleri kontrol edilir; Solana SPL token-account adresleri strict transaction metadata uzerinden ana sahip adresine eslenir. Eksiksiz address index hazir degilse chain listener'lar fail-closed kalir. Deposit worker eski fact'ler transaction veya ledger state'ine ulasmadan once asset ve internal-transfer kontrollerini tekrarlar. Listener path'i payment'i paid yapmaz, ledger entry yazmaz, webhook enqueue etmez ve sweep job yaratmaz. Finality gate tamamlaninca deposit fact worker transaction/deposit lifecycle'ini, ledger posting, payment matching ve money event outbox akisini idempotent sekilde ilerletir.

Canlı ortamda gateway ve wallet provider hazırlığını doğrulamak için `GET /api/v1/common/readiness` kullanılmalıdır. Endpoint DB erişimini, production migration politikasını ve `GATEWAY_DB_MIGRATION_VERSION` kanıtını, signer üretim kapısını, backlog/drift durumunu, tüm chain kayıtlarını, listener worker kayıtlarını, Trust Wallet Core HD wallet türetmesini ve son provider health snapshot'larını kontrol eder; eksik veya bozuk bağımlılık varsa `503` döner. Provider health worker raw RPC URL'leri saklamaz veya readiness/metric çıktısına yazmaz.

Prometheus uyumlu operasyon metrikleri için `GET /metrics` kullanılabilir. Endpoint money event outbox backlog, webhook delivery backlog, sweep job backlog, withdrawal/refund backlog, reconciliation drift, chain worker count, chain state block/slot, provider health/lag/latency/failover, wallet address lookup row count, migration/signer readiness ve external signer adapter readiness gauge'larını döndürür. `APP_ENV=production` altında `Authorization: Bearer <METRICS_BEARER_TOKEN>` zorunludur. Runbook: `docs/money-path-observability-runbook.md`.

HTTP sınırında her response `X-Request-ID` taşır. Request logları method, path, route, status, duration, error type ve request id ile sınırlıdır; query string, request body, `Authorization`, API key, signature veya secret değerleri loglanmaz. Panic durumunda response sanitize edilmiş `500` ve `request_id` içerir.

## Güvenlik Notları

- `MASTER_KEY` ve `MNEMONIC_PHRASE` production ortamında secret manager, KMS veya benzeri güvenli bir sistemden sağlanmalıdır.
- `APP_ENV=production` altında startup `AutoMigrate` açık bırakılmamalıdır; `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true` readiness tarafından production launch blocker olarak raporlanır.
- `/metrics` production ortamında sadece private network veya reverse proxy allowlist arkasından ve `METRICS_BEARER_TOKEN` ile açılmalıdır.
- Production custody için process içi software signer veya local private-key signing kullanılamaz. KMS/HSM/MPC/Vault veya eşdeğer external signer adapter'ı chain-specific signing'i tamamlamadan yüksek hacimli müşteri fonu tutulmamalıdır.
- Admin parolası güçlü ve benzersiz olmalıdır.
- Merchant portal/admin formları ve public API uçları production öncesinde portal JWT, rate limit ve reverse proxy ayarlarıyla ayrıca doğrulanmalıdır.
- Webhook imzaları `X-Gateway-Signature`, `X-Gateway-Timestamp` ve `X-Gateway-Event` header'ları üzerinden doğrulanacak şekilde tasarlanmıştır.
- Public erişimde HTTPS, güvenli cookie ayarları ve sınırlı CORS origin listesi kullanılmalıdır.

## Geliştirme Komutları

Go testleri:

```bash
go test ./...
```

Exchange webhook/bakiye entegrasyon smoke testi:

```bash
cd ../exchange
./scripts/smoke_gateway_deposit.sh
```

Bu test admin login gerektirmez; gateway domain webhook secret'ını decrypt eder, exchange callback'e imzalı BTC deposit gönderir ve exchange DB'deki bakiyenin arttığını doğrular.

Trust Wallet Core submodule'ü hazır değilse önce `git submodule update --init --recursive third_party/trustwallet/wallet-core`, native build hazır değilse `./scripts/build_wallet_core.sh` çalıştırılmalıdır. `walletcorefallback` build tag'i yalnızca dar kapsamlı lokal debug içindir; production wallet üretimi veya transfer signing için kullanılmamalıdır.

CSS build:

```bash
npm run build:css
```

Swagger build:

```bash
swag init -g main.go -o docs
```

Migration:

```bash
go run . -migrate
```

Uygulama:

```bash
go run .
```

## Yeni Chain veya Token Kaydı

Yeni chain eklemek için:

1. `blockchain/chains/` altında chain implementasyonunu ekleyin veya mevcut EVM-compatible yapıyı kullanın.
2. Chain ID ve slug bilgisini `constants/chains.go` dosyasına ekleyin.
3. Chain'i `application/configuration/chains.go` içindeki factory'ye register edin.
4. Gerekirse listener worker seçimini `main.go` içinde doğrulayın.
5. İlgili coin görselini `static/chains/` altına ekleyin.

Yeni token eklemek için:

1. Token tipini `asset/` içindeki uygun constructor ile kaydedin.
2. Kaydı `application/configuration/assets.go` dosyasına ekleyin.
3. Token sembolü, decimals, token address/mint ve chain ID değerlerini doğrulayın.
4. Gerekirse `static/coins/` altına görsel ekleyin.

## Yol Haritası

Detaylı teknik audit ve öncelikli geliştirme işleri için `ROADMAP.md` dosyasına bakın. Özellikle production kullanımı öncesinde finality gate, reorg handling, portal JWT, Redis tabanlı rate limit, webhook URL re-validation, structured logging ve KMS/Vault entegrasyonu gibi başlıklar ayrıca değerlendirilmelidir.
