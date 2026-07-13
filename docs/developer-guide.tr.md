# Geliştirici Rehberi

Dil: [English](developer-guide.md) | **Türkçe**

Bu rehber Gateway projesini güvenli şekilde geliştirmek için hazırlanmıştır. Feature branch açarken veya para hareketiyle ilgili bir değişikliği review ederken ana README, entegrasyon rehberi ve module-boundary dokümanlarıyla birlikte kullanın.

## Önce Okunacaklar

- Kurulum, environment değişkenleri, runtime komutları ve API özeti için `README.tr.md`.
- Deposit, payment, ledger, outbound, webhook, reconciliation, listener veya worker değişikliklerinden önce `docs/module-boundaries.md`.
- Kalıcı state veya tablo değişikliklerinden önce `docs/production-migration-discipline.md`.
- `/api/v1` partner API davranışı değişiyorsa `docs/integration-guide.md`.
- Webhook veya money event ekleniyor/değişiyorsa `docs/money-event-catalog.md`.

## Lokal Kurulum

Gateway; PostgreSQL, GORM, Fiber v3, server-rendered HTML, Tailwind CSS ve Trust Wallet Core kullanan Go tabanlı bir modular monolith'tir.

Gerekli araçlar:

- Go `1.25.4`
- PostgreSQL
- Node.js ve npm
- Git submodule desteği
- Trust Wallet Core için `scripts/build_wallet_core.sh` tarafından kullanılan native build araçları

İlk kurulum:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
go mod download
npm install
./scripts/build_wallet_core.sh
npm run build:css
cp .env.sample .env
```

Trust Wallet Core submodule'ü zorunludur. `go.mod` içinde `tw` paketi `./third_party/trustwallet/wallet-core/samples/go` yoluna replace edilir. `go test`, `go run` veya `go mod download` eksik `tw` replacement directory hatası verirse replace satırını kaldırmayın; submodule'ü başlatın.

`.env` içinde sadece development secret'ları ve development mnemonic kullanın. Gerçek key, production mnemonic, webhook secret, API secret veya RPC credential commit etmeyin.

## Geliştirme Döngüsü

Lokal veritabanını hazırlayın:

```bash
go run . -migrate
go run . -seed
```

Migration ve seed birlikte çalışacaksa:

```bash
go run . -install
```

Uygulamayı doğrudan başlatın:

```bash
go run .
```

Go ve HTML değişiklikleri için auto-reload:

```bash
go install github.com/air-verse/air@latest
npm run dev
```

Air kullanırken `http://localhost:3000` adresini açın. Air, `.env` içindeki uygulama portuna proxy yapar; varsayılan değer `PORT=:3001`. `PORT` değişirse `.air.toml` içindeki `proxy.app_port` değerini de güncelleyin.

Tailwind kaynak CSS'i değiştirirken ikinci terminalde watcher çalıştırın:

```bash
npm run dev:css
```

View veya CSS değişikliklerini commit etmeden önce generated/minified CSS'i üretin:

```bash
npm run build:css
```

## Runtime Yapısı

Uygulama başlangıcı `main.go` üzerinden ilerler:

- `.env` yüklenir.
- `DATABASE_URL` ile veritabanı başlatılır.
- `routes.NewRouter`; Fiber app, repository'ler, servisler, chain factory, asset registry, payment hub, portallar, `/api/v1`, metrics, Swagger ve docs route'larını kurar.
- `services/database.Migrate`, production dışındaki ortamlarda startup migration çalıştırır; production'da schema doğrular.
- Worker supervisor periodic worker'ları başlatır.
- Address index load, admin bootstrap, chain infrastructure ve HTTP server başlar.

Ana composition root `application.CORE` objesidir. Yeni paylaşılan bağımlılıklar gizli package global'larıyla değil composition root ve `api/routes/routes.go` üzerinden bağlanmalıdır.

## Kod Sahipliği

Mevcut package sınırlarını takip edin:

| Alan | Değiştirilecek yer |
| --- | --- |
| HTTP routing | `api/routes/routes.go`, legacy command path için `constants/commands.go` |
| HTTP handler'lar | `api/handlers/` |
| Request/response DTO ve validasyon | `types/` |
| Business servisleri | `services/` |
| Veritabanı erişimi | `repositories/` |
| GORM modelleri | `models/` |
| Chain konfigürasyonu | `constants/chains.go`, `application/configuration/chains.go`, `blockchain/chains/` |
| Asset/token konfigürasyonu | `application/configuration/assets.go`, `asset/` |
| Periodic/background işler | `workers/`, `services/*`, `workers/supervisor` |
| Merchant/admin/checkout UI | `views/`, `views/assets/` |
| OpenAPI çıktısı | `docs/swagger.yaml`, `docs/swagger.json`, `docs/docs.go` |

Para hareketi değişikliklerinde `docs/module-boundaries.md` içindeki sahiplik kuralları korunmalıdır. Chain listener'lar sadece fact kaydeder; payment, ledger balance, webhook veya sweep state'ini doğrudan değiştirmez. Deposit, payment, ledger, outbound, webhook ve reconciliation sınırları servis, repository, worker command veya money event üzerinden geçilmelidir.

## Sık Yapılan Değişiklikler

### V1 API Endpoint Ekleme veya Değiştirme

1. DTO ve validasyonları `types/` altında ekleyin veya güncelleyin.
2. Handler davranışını `api/handlers/` içinde yazın.
3. Route'u `api/routes/routes.go` içinde kaydedin.
4. Endpoint'in read-only API-key erişimi mi, mutating HMAC-signed erişim mi istediğine karar verin.
5. Para hareketi veya durable operasyon oluşturan endpoint'lerde scope, IP allowlist, idempotency ve replay protection davranışını ekleyin.
6. Swagger annotation'larını güncelleyip docs üretin:

```bash
swag init -g main.go -o docs
```

7. Partner-facing davranış değişiyorsa `docs/integration-guide.md` dosyasını güncelleyin.
8. Handler, auth, repository ve contract testlerini ekleyin.

### Database Modeli Ekleme veya Değiştirme

1. GORM modelini `models/` altında ekleyin veya güncelleyin.
2. Repository metotlarını `repositories/` altında yazın; query ve transaction davranışını handler içine taşımayın.
3. Modeli `services/database.autoMigrateModels` listesine ekleyin.
4. `services/database.VerifySchema` helper'ları ve testlerinde schema verification kapsamı ekleyin.
5. `services/dbmigrations/migrations.go` içine summary, forward plan, lock impact, backfill, rollback, verification query ve verification command içeren versioned artifact ekleyin.
6. Artifact listesi değişirse `docs/production-migration-discipline.md` dosyasını güncelleyin.
7. Idempotency, locking, conflict ve lifecycle transition davranışları için repository testleri ekleyin.

Production schema değişiklikleri sadece startup `AutoMigrate` değişikliği değildir. Production rollout, backfill, verification ve rollback kanıtı açık tutulmalıdır.

### Worker Ekleme

1. Mümkünse saf işi `RunOnce` tarzı bir servis metodunda tutun.
2. Loop'ların `context.Context` kabul etmesini ve cancellation'da hızlı kapanmasını sağlayın.
3. Lifecycle sahipliğini `workers/supervisor` üzerinden kaydedin.
4. Tek process'in sahiplenmesi gereken işlerde `WorkerLeaseRepo` kullanın.
5. External veya cross-module işi acknowledge etmeden önce progress'i persist edin.
6. Cancellation, retry, idempotency, lock expiry ve partial failure recovery testleri ekleyin.

### Chain Ekleme

1. Chain ID, isim, logo slug ve testnet sınıflandırmasını `constants/chains.go` içine ekleyin.
2. `blockchain/chains/` altında chain implementasyonunu yazıp ortak chain interface'ini sağlayın.
3. Chain'i alias'larıyla birlikte `application/configuration/chains.go` içinde kaydedin.
4. Listener varsa RPC env handling ve listener start-block policy desteğini ekleyin.
5. Native asset ve token deployment kayıtlarını `application/configuration/assets.go` içine ekleyin.
6. Chain discovery ve address validation için route/API/readiness/test kapsamı ekleyin.
7. UI logo kullanacaksa static chain/coin asset'lerini ekleyin.

### Asset veya Token Deployment Ekleme

1. `application/configuration/assets.go` içindeki `asset.AssetDefinition` kaydını ekleyin veya güncelleyin.
2. Doğru chain ID, contract address veya mint, decimals, native flag, symbol ve enabled flag kullanın.
3. Alias'ları yalnızca proje wrapped veya alternatif sembolleri canonical asset gibi ele alacaksa ekleyin.
4. CoinGecko sembolü desteklemiyorsa price fallback env desteğini ekleyin.
5. Deployment; grouping, selection, logo veya API çıktısını etkiliyorsa asset registry testlerini ekleyin/güncelleyin.

### Webhook veya Money Event Değiştirme

1. Catalog'u `services/webhook/event_catalog.go` içinde güncelleyin.
2. `docs/money-event-catalog.md` dosyasını güncelleyin.
3. Event ID stability ve idempotency davranışını koruyun.
4. Webhook delivery'nin at-least-once ve retry-safe kalmasını sağlayın.
5. Event shape, ordering, replay ve diagnostics davranışı için contract testleri ekleyin.

### Merchant/Admin/Checkout View Değiştirme

1. Template'leri `views/` altında düzenleyin.
2. CSS/JS dosyalarını `views/assets/` altında düzenleyin.
3. Tailwind input değiştiyse `npm run build:css` çalıştırın.
4. `views_test.go` veya handler testlerinde render testlerini ekleyin/güncelleyin.
5. Development mode'da checkout ve portal route'larını doğrulayın; HTML reload ve no-cache static asset davranışı burada görünür.

## Test

Geliştirme sırasında hedefli test çalıştırın:

```bash
go test ./services/deposits ./repositories -run TestName
```

Tüm Go testleri:

```bash
go test ./...
```

Native Trust Wallet Core yoksa veya CI tarzı izole wallet modu gerekiyorsa fallback regression:

```bash
GATEWAY_REGRESSION_MODE=fallback ./scripts/regression.sh
```

Wallet generation, signing, address validation, chain implementasyonu veya production-critical money movement değiştiyse native regression:

```bash
./scripts/regression.sh
```

Bazı repository ve integration testleri PostgreSQL ister ve DSN verilmezse skip olur. Disposable database ile çalıştırın:

```bash
TEST_DATABASE_URL="host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway_test sslmode=disable" go test ./repositories ./services/database ./workers/indexer ./services/txrescan
```

Go vet'i regression script üzerinden veya doğrudan çalıştırın:

```bash
go vet ./...
```

## Güvenlik Kuralları

- Server'ı `-tags walletcorefallback` ile çalıştırmayın; `main.go` fallback server run'larını yalnızca dar debug override ile kabul eder.
- Production custody için `SIGNER_MODE=software` kullanmayın. Production signing KMS, HSM, MPC veya Vault gibi external signer mode arkasında kalmalıdır.
- Chain listener içine money-path state change eklemeyin.
- Bir repository erişilebilir diye handler içinden cross-module tablo güncellemesi yazmayın.
- Payment, payout, refund, sweep, webhook veya ledger işlerinde idempotency'yi bypass etmeyin.
- Mutating partner API route'larında HMAC signing, timestamp check, API scope, IP allowlist veya replay protection davranışını zayıflatmayın.
- `.env`, private key, mnemonic, API secret, webhook secret, production RPC credential veya generated local DB dump commit etmeyin.

## Pull Request Checklist

- Gerekli dokümanlar güncellendi: README, integration guide, money event catalog, runbook veya migration discipline.
- Yeni persisted state; model, migration artifact, schema verification ve repository testlerine eklendi.
- Yeni partner davranışı için API contract ve Swagger güncellendi.
- Yeni worker cancellable, idempotent, observable ve supervisor-owned.
- Money-path değişiklikleri module ownership sınırlarına uyuyor.
- CSS değişikliklerinden sonra `views/assets/tailwind.css` yeniden üretildi.
- Testler dar package suite'i ve cross-module etki varsa daha geniş regression kapsamını içeriyor.
