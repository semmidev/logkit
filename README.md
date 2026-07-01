# logkit

High-performance, structured, and secure Go logging library built on top of `log/slog` (standard library, Go 1.21+). Designed for low overhead, request-context propagation, and defense-in-depth safety.

---

## ⚡ Fitur Utama

- **Zero-Allocation Context Propagation:** Propagasi request ID, correlation ID, trace/span ID, dan fields kustom via `context.Context` tanpa alokasi heap di hot-path.
- **High-Performance Async Logging:** `slogx.AsyncWriter` untuk buffering log secara asinkron (mengurangi overhead syscall I/O seperti Pino).
- **Advanced Pino-like Redaction:** Konfigurasi `RedactConfig` untuk penyaringan data sensitif tingkat lanjut menggunakan dot-notation path, wildcards, custom censor, atau penghapusan key (`Remove: true`).
- **Dynamic Caller Stack Skipping:** Dukungan `WithCallerSkip(n)` untuk menyesuaikan kedalaman stack trace saat membungkus logger pada helper/wrapper function.
- **Wide Events & Tail Sampling:** Akumulasi log dalam satu lifecycle request (canonical log line) dan penentuan sampling keputusan di akhir request via `TailSampler`.
- **Secure by Construction:** Pemisahan tegas pesan error publik dengan internal (`secure.SafeError`) serta proteksi data sensitif di level tipe (`secure.Secret[T]`).

---

## 📂 Struktur Package

```
logkit/
├── logkit.go, levels.go   # Logger core (slog wrapper) & Custom levels (Trace, Fatal)
├── logctx/                # Context-propagation (request/correlation ID)
├── event/                 # Wide event / Canonical log line builder
├── secure/                # SafeError & Secret[T] (redaksi otomatis level tipe)
├── sampler/               # Tail sampling untuk wide event
├── slogx/                 # AsyncWriter, JSON/Console handlers, & Advanced Redactor
└── middleware/
    ├── httpmw/            # HTTP Middleware (chi, standard mux, etc.)
    └── grpcmw/            # gRPC Interceptors (Go module terpisah)
```

---

## 🚀 Quick Start

### 1. Inisialisasi Logger (dengan Async Writer & Redaction)

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/slogx"
)

func main() {
	// 1. Setup Async Writer (High-Performance/Pino-like)
	asyncWriter := slogx.NewAsyncWriter(os.Stdout, 8192, 100*time.Millisecond)
	defer asyncWriter.Close()

	// 2. Setup Logger dengan Advanced Path Redaction
	logger := logkit.New(logkit.Config{
		Format:      logkit.FormatJSON,
		Writer:      asyncWriter,
		Level:       slog.LevelDebug,
		ServiceName: "user-service",
		Version:     "1.0.0",
		Environment: "production",
		Redact: &logkit.RedactConfig{
			Paths: []string{
				"password",                  // Redaksi key "password" di level mana pun
				"req.headers.authorization", // Redaksi nested path spesifik
				"card.number",               // Redaksi credit card di dalam group card
			},
			Censor: "[SENSITIF]",
		},
	})
	defer logger.Sync() // Pastikan semua log tertulis sebelum app exit

	ctx := context.Background()
	logger.Info(ctx, "Server started successfully")
}
```

### 2. Context Propagation & Enrichment
```go
// Stamp IDs di middleware
ctx = logctx.WithRequestID(ctx, logctx.NewID())
ctx = logctx.WithCorrelationID(ctx, logctx.NewID())
ctx = logctx.WithFields(ctx)

// Tambah context bisnis di sub-function
logctx.Enrich(ctx, "user_id", "u_999", "tier", "premium")

logger.Info(ctx, "User purchased item")
// Hasil output log otomatis menyertakan: request_id, correlation_id, user_id, dan tier.
```

### 3. Custom Caller Skipping (Helper/Wrapper)
```go
func logErrorHelper(ctx context.Context, logger *logkit.Logger, err error) {
    // Lewati 1 frame tambahan agar file:line menunjuk ke pemanggil logErrorHelper
    logger.WithCallerSkip(1).Error(ctx, "database failed", "error", err)
}
```

---

## ⚙️ Panduan Ekstensi

| Tujuan Ekstensi | Metode |
|---|---|
| Mengirim log ke loket pihak ketiga (Loki, Datadog) | Implementasi standard `slog.Handler` lalu berikan ke `Config.Handler` |
| Mengganti algoritma generator ID | Override `logctx.NewID` lewat config `GenerateID` di middleware |
| Kriteria sampling kustom | Implementasi `sampler.Decider` |
| Filter/Ubah data sebelum ditulis | Berikan fungsi kustom pada `slogx.HandlerOptions.ReplaceAttr` |

---

## 🛠️ Testing & Running

```bash
# Menjalankan unit tests & benchmark performa
go test ./... -v
go test -bench=. -benchmem ./...

# Menjalankan contoh aplikasi
go run ./examples/basicapp
go run ./examples/httpapp
```
