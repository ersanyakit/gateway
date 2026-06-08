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
- Trust Wallet Core native library build'i (CGo, clang/cmake ve `scripts/build_wallet_core.sh`)
- Chain listener'ların sağlıklı çalışması için ilgili ağlara RPC erişimi

## Kurulum

Bağımlılıkları indirin:

```bash
go mod download
npm install
```

Trust Wallet Core native library dosyalarını üretin. Cüzdan mnemonic doğrulama, HD private key türetme ve adres üretimi default olarak Trust Wallet Core üzerinden yapılır:

```bash
./scripts/build_wallet_core.sh
```

Tailwind CSS dosyasını üretin:

```bash
npm run build:css
```

Minimum `.env` örneği:

```env
DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
PORT=:4001
APP_ENV=development
ALLOW_PRIVATE_WEBHOOK_URLS=true
MASTER_KEY=32-byte-or-longer-secret
MNEMONIC_PHRASE="your bip39 mnemonic phrase"
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=change-this-password
ADMIN_NAME=Gateway Admin
```

Veritabanı migration'larını çalıştırın:

```bash
go run main.go -migrate
```

Migration ve seed işlemini birlikte çalıştırmak için:

```bash
go run main.go -install
```

Uygulamayı başlatın:

```bash
go run main.go
```

`PORT` değeri Fiber formatında verilmelidir. Örnek: `:4001`.

## Ortam Değişkenleri

Zorunlu veya kritik değişkenler:

| Değişken | Açıklama |
| --- | --- |
| `DATABASE_URL` | PostgreSQL bağlantı adresi. Uygulama bu değer olmadan başlamaz. |
| `PORT` | Fiber listen adresi. Örnek: `:4001`. |
| `APP_ENV` | Çalışma ortamı. `production` değerinde bazı güvenlik kontrolleri sıkılaşır. Lokal geliştirme için `development` kullanılabilir. |
| `ALLOW_PRIVATE_WEBHOOK_URLS` | Lokal geliştirmede `localhost`, `127.0.0.1` veya özel ağ IP'lerine webhook göndermeye izin verir. `APP_ENV=production` iken dikkate alınmaz. |
| `MASTER_KEY` | API secret, webhook secret ve credential şifreleme işlemlerinde kullanılır. |
| `MNEMONIC_PHRASE` | Trust Wallet Core ile HD wallet üretimi için BIP39 mnemonic. |
| `ADMIN_EMAIL` | Bootstrap admin hesabı için e-posta. |
| `ADMIN_PASSWORD` | Bootstrap admin hesabı için parola. |
| `ADMIN_NAME` | Bootstrap admin görünen adı. |

Opsiyonel servis değişkenleri:

| Değişken | Varsayılan / Açıklama |
| --- | --- |
| `CORS_ALLOWED_ORIGINS` | Virgülle ayrılmış izinli origin listesi. Boş origin isteklerine izin verilir. |
| `API_KEY_RATE_LIMIT_PER_MINUTE` | `/api/v1` için dakika başına limit. Varsayılan: `120`. |
| `WEBHOOK_RETRY_INTERVAL` | Webhook retry worker aralığı. Varsayılan: `30s`. |
| `WEBHOOK_MAX_ATTEMPTS` | Webhook delivery maksimum deneme sayısı. |
| `TRANSACTION_FINALITY_INTERVAL` | Pending transaction finality worker aralığı. Varsayılan: `20s`. |
| `FINALITY_CONFIRMATIONS_DEFAULT` | Genel confirmation fallback değeri. |
| `CHAIN_<id>_CONFIRMATIONS` | Chain ID bazlı confirmation override. |
| `<CHAIN_NAME>_CONFIRMATIONS` | Chain slug bazlı confirmation override. Örnek: `ETHEREUM_CONFIRMATIONS`. |
| `COINGECKO_BASE_URL` | CoinGecko API adresi. Varsayılan: `https://api.coingecko.com/api/v3`. |
| `COINGECKO_CACHE_TTL` | Fiyat cache süresi. |
| `COINGECKO_API_KEY` | CoinGecko API anahtarı. |
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
| `TRON_GRPC_ENDPOINT` / `TRON_GRPC_ENDPOINTS` | TRON listener gRPC endpoint ayarları. |
| `TRON_PRO_API_KEY` | TRON API erişimi için opsiyonel anahtar. |

Gas/prefund ayarları:

| Değişken | Açıklama |
| --- | --- |
| `EVM_GAS_THRESHOLD_WEI` | EVM cüzdan gas eşiği. |
| `EVM_GAS_PREFUND_WEI` | EVM prefund miktarı. |
| `TRON_GAS_THRESHOLD_SUN` | TRON gas eşiği. |
| `TRON_GAS_PREFUND_SUN` | TRON prefund miktarı. |
| `SOLANA_GAS_THRESHOLD_LAMPORTS` | Solana gas eşiği. |
| `SOLANA_GAS_PREFUND_LAMPORTS` | Solana prefund miktarı. |

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
X-Gateway-Signature: sha256=<hmac_sha256(timestamp + raw_body)>
```

Common endpoint'ler:

- `GET /api/v1/common/status`
- `GET /api/v1/common/balance`
- `GET /api/v1/common/prices`
- `GET /api/v1/common/currencies`
- `GET /api/v1/common/fiat-currencies`
- `GET /api/v1/common/networks`

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
curl -X POST http://localhost:4001/api/v1/payment/create \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-secret>" \
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
curl -X POST http://localhost:4001/api/v1/payment/static-address \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-secret>" \
  -d '{
    "user_id": "customer_42",
    "chain_id": 1,
    "symbol": "USDT",
    "label": "Main wallet"
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

Migration işlemi `services/database/database.go` içindeki `AutoMigrate` ile aşağıdaki ana tabloları yönetir:

- `chain_states`
- `domains`
- `merchants`
- `transactions`
- `ledger_entries`
- `wallets`
- `products`
- `payment_sessions`
- `idempotency_keys`
- `webhook_deliveries`
- `withdrawal_requests`
- `refunds`
- `price_quotes`
- `reconciliation_jobs`
- `activity_logs`
- `admins`

Migration başında PostgreSQL `uuid-ossp` extension'ı etkinleştirilir.

## Worker'lar

Uygulama başlarken şu arka plan süreçlerini başlatır:

- Address index yükleme ve eksik adres backfill işlemi
- Bootstrap admin hesabı oluşturma
- Webhook retry worker
- Payment session expiry worker
- Pending transaction finality worker
- Bitcoin, Ethereum/EVM, Solana ve TRON listener worker'ları

Listener'lar transaction event'lerini dispatcher üzerinden publish eder. Dispatcher event'i ilgili wallet ile eşleştirir, transaction kaydını oluşturur, payment session durumunu günceller ve gerekiyorsa webhook gönderir.

## Güvenlik Notları

- `MASTER_KEY` ve `MNEMONIC_PHRASE` production ortamında secret manager, KMS veya benzeri güvenli bir sistemden sağlanmalıdır.
- Admin parolası güçlü ve benzersiz olmalıdır.
- Merchant portal/admin formları ve public API uçları production öncesinde CSRF, rate limit ve reverse proxy ayarlarıyla ayrıca doğrulanmalıdır.
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

Trust Wallet Core native build'i hazır değilse önce `./scripts/build_wallet_core.sh` çalıştırılmalıdır. Sadece lokal debug için fallback provider kullanılacaksa Go komutlarına `-tags walletcorefallback` eklenebilir; production wallet üretimi için kullanılmamalıdır.

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
go run main.go -migrate
```

Uygulama:

```bash
go run main.go
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

Detaylı teknik audit ve öncelikli geliştirme işleri için `ROADMAP.md` dosyasına bakın. Özellikle production kullanımı öncesinde finality gate, reorg handling, CSRF, Redis tabanlı rate limit, webhook URL re-validation, structured logging ve KMS/Vault entegrasyonu gibi başlıklar ayrıca değerlendirilmelidir.
