# Gateway

Gateway, Go ile geliştirilmiş çok zincirli bir kripto ödeme geçidi ve merchant yönetim platformudur. Proje; merchant kaydı, domain bazlı API anahtarları, checkout oturumları, statik cüzdan adresleri, ödeme takibi, payout/refund akışları, webhook bildirimi, admin paneli ve blockchain listener worker'larını tek uygulama içinde birleştirir.

Uygulama Go Fiber v3, GORM ve PostgreSQL üzerine kuruludur. Frontend tarafında server-rendered HTML view'ları, Tailwind CSS ve checkout/admin/merchant ekranları bulunur.

## Temel Özellikler

- Çok zincirli cüzdan üretimi ve adres doğrulama
- Checkout linki ve invoice ekranı ile kripto ödeme alma
- Merchant portalı üzerinden domain, ürün, invoice ve withdrawal yönetimi
- Admin paneli üzerinden merchant, withdrawal, refund, webhook replay ve admin kullanıcı yönetimi
- API key veya Bearer token ile `/api/v1` merchant API erişimi
- Idempotency desteği olan ödeme oluşturma akışları
- Blockchain listener worker'ları ile on-chain transaction izleme
- Payment session durumu, websocket status takibi ve QR kod üretimi
- Webhook delivery, retry ve replay altyapısı
- Ledger, withdrawal request, refund, price quote ve activity log modelleri
- CoinGecko fiyat servisi entegrasyonu
- Swagger/OpenAPI çıktısı

## Desteklenen Ağlar ve Varlıklar

Zincir kayıtları `application/configuration/chains.go`, varlık kayıtları `application/configuration/assets.go` dosyasında tutulur.

Desteklenen ağlar:

- Bitcoin
- Ethereum
- TRON
- Solana
- Avalanche C-Chain
- BNB Chain
- Chiliz
- Chiliz Spicy testnet
- Base
- Arbitrum One
- Unichain

Örnek kayıtlı varlıklar:

- Native varlıklar: BTC, ETH, TRX, SOL, AVAX, BNB, CHZ
- ERC-20/SPL/TRC-20 tokenlar: USDT, USDC, WBTC, WETH ve proje özelinde kayıtlı tokenlar
- Wrapped token alias'ları: WBTC -> BTC, WETH -> ETH, WBNB -> BNB, WCHZ -> CHZ

## Proje Yapısı

```text
.
├── api/
│   ├── handlers/          # HTTP handler'ları, checkout, merchant portal, admin ve v1 API
│   ├── middleware/        # Security header, CORS ve rate limit middleware'leri
│   ├── router/            # Action router yardımcıları
│   └── routes/            # Fiber route kayıtları ve Swagger route'ları
├── application/
│   └── configuration/     # Chain factory ve asset registry konfigürasyonu
├── asset/                 # Varlık tipleri ve registry
├── blockchain/            # Chain interface, factory ve zincir implementasyonları
├── constants/             # Komut, ürün ve chain sabitleri
├── contracts/             # ERC20 ve Multicall sözleşme bağlayıcıları
├── docs/                  # Swagger/OpenAPI çıktıları
├── helpers/               # Şifreleme, credential ve yardımcı fonksiyonlar
├── models/                # GORM modelleri
├── repositories/          # Veritabanı erişim katmanı
├── services/              # Database, pricing, realtime, system ve webhook servisleri
├── static/                # Chain/coin görselleri
├── types/                 # Request/response DTO'ları
├── views/                 # Merchant portal, admin, gateway ve invoice HTML view'ları
└── workers/               # Listener, dispatcher ve address index worker'ları
```

## Çalışma Akışı

1. Merchant portalından veya internal endpoint'lerden oluşturulur.
2. Merchant için domain kaydı açılır ve API secret/webhook ayarları tutulur.
3. Merchant `/api/v1/payment/create` veya panel üzerinden invoice oluşturur.
4. Kullanıcı checkout ekranında desteklenen ağ/varlık seçimi yapar.
5. Uygulama ödeme oturumu için deposit wallet bilgisini ve QR kodu gösterir.
6. Blockchain listener worker'ları ilgili ağları izler.
7. Gelen transaction wallet/address index ile eşleşirse transaction kaydedilir.
8. Payment session tutar ve asset bilgisiyle eşleştiğinde ödeme durumu güncellenir.
9. Webhook notifier merchant domain webhook adresine imzalı event gönderir.
10. Başarısız webhook denemeleri retry worker ile tekrar denenir.

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
MASTER_KEY=32-byte-or-longer-secret
PORTAL_JWT_SECRET=32-byte-or-longer-portal-jwt-secret
MNEMONIC_PHRASE="your bip39 mnemonic phrase"
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=change-this-password
ADMIN_NAME=Gateway Admin
TRON_GRPC_ENDPOINTS=grpc.trongrid.io:50051
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
| `SIGNER_MODE` | `software`, `kms`, `hsm`, `mpc`, `vault` veya external custody modu. `software` sadece development içindir; production signing gerçek external signer adapter'ı olmadan hard-fail eder. |
| `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION` | Legacy risk marker. `true` olsa bile `APP_ENV=production` altında software signing'e izin vermez ve production readiness'ı geçirmez. |
| `METRICS_BEARER_TOKEN` | `/metrics` Prometheus endpoint'i için bearer token. `APP_ENV=production` iken zorunludur; boş bırakılırsa endpoint 503 döner. |
| `PORTAL_JWT_SECRET` | Merchant/admin portal mutasyon JWT imzalama secret'ı. Production'da stabil olmalıdır. Yoksa `DEALER_SESSION_SECRET`, `SESSION_SECRET` veya `MASTER_KEY` fallback kullanılır. |
| `DEALER_SESSION_SECRET` / `SESSION_SECRET` | Merchant/admin session imzalama fallback secret'ları. Production'da rastgele runtime secret'a düşülmemelidir. |
| `MASTER_KEY` | API secret, webhook secret ve credential şifreleme işlemlerinde kullanılır. |
| `MNEMONIC_PHRASE` | Trust Wallet Core ile BIP39 doğrulama, HD wallet üretimi ve development signing için kullanılan mnemonic. Production custody için secret manager/KMS sınırında tutulmalıdır. |
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
| `TRON_JSONRPC_URLS` | TRON JSON-RPC endpoint listesi. |
| `TRON_HTTP_ENDPOINT` / `TRON_HTTP_ENDPOINTS` | TRON HTTP API endpoint ayarları. |
| `TRON_GRPC_ENDPOINT` / `TRON_GRPC_ENDPOINTS` | TRON listener gRPC endpoint ayarları. |
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
- `admins`

Migration başında PostgreSQL `uuid-ossp` extension'ı etkinleştirilir. `APP_ENV=production` iken startup `AutoMigrate` varsayılan olarak kapalıdır; uygulama yalnızca beklenen schema kolonlarını doğrular. Production DDL, ayrı bir versioned migration süreciyle ve bakım penceresinde yürütülmelidir.

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

Listener'lar transaction event'lerini dispatcher üzerinden publish eder. Dispatcher path'i once durable `chain_facts` kaydi olusturur; listener path'i payment paid yapmaz, ledger entry yazmaz, webhook enqueue etmez ve sweep job yaratmaz. Deposit fact worker bu fact'leri wallet ownership ile eslestirir, finality gate tamamlaninca transaction/deposit lifecycle'i, ledger posting, payment matching ve money event outbox akisini idempotent sekilde ilerletir.

Canlı ortamda gateway ve wallet provider hazırlığını doğrulamak için `GET /api/v1/common/readiness` kullanılmalıdır. Endpoint DB erişimini, production migration politikasını, signer üretim kapısını, backlog/drift durumunu, tüm chain kayıtlarını, listener worker kayıtlarını, Trust Wallet Core HD wallet türetmesini ve canlı RPC/gRPC son blok erişimini kontrol eder; eksik veya bozuk bağımlılık varsa `503` döner.

Prometheus uyumlu operasyon metrikleri için `GET /metrics` kullanılabilir. Endpoint webhook delivery backlog, sweep job backlog, reconciliation drift, chain worker count, chain state block/slot ve migration/signer readiness gauge'larını döndürür. `APP_ENV=production` altında `Authorization: Bearer <METRICS_BEARER_TOKEN>` zorunludur.

HTTP sınırında her response `X-Request-ID` taşır. Request logları method, path, route, status, duration, error type ve request id ile sınırlıdır; query string, request body, `Authorization`, API key, signature veya secret değerleri loglanmaz. Panic durumunda response sanitize edilmiş `500` ve `request_id` içerir.

## Güvenlik Notları

- `MASTER_KEY` ve `MNEMONIC_PHRASE` production ortamında secret manager, KMS veya benzeri güvenli bir sistemden sağlanmalıdır.
- `APP_ENV=production` altında startup `AutoMigrate` açık bırakılmamalıdır; `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true` readiness tarafından production launch blocker olarak raporlanır.
- `/metrics` production ortamında sadece private network veya reverse proxy allowlist arkasından ve `METRICS_BEARER_TOKEN` ile açılmalıdır.
- Production custody için process içi software signer kullanılamaz. KMS/HSM/MPC/Vault veya eşdeğer external signer entegrasyonu tamamlanmadan yüksek hacimli müşteri fonu tutulmamalıdır.
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
