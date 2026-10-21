# logkit

Library logging Go yang terstruktur, aman, dan berkinerja tinggi, dibangun di atas `log/slog` (pustaka standar, Go 1.21+). logkit menambahkan tiga hal di atas slog: propagasi konteks request lewat `context.Context`, wide event / canonical log line, dan pengamanan data sensitif di level tipe.

## Fitur

- **Propagasi konteks tanpa alokasi** — request ID, correlation ID, trace/span ID, dan fields kustom dibawa lewat `context.Context`; setiap panggilan logger di dalam call graph otomatis memuatnya, tanpa menambah parameter `*Logger` ke setiap fungsi. Hot-path tanpa alokasi heap.
- **Level dinamis di runtime** — `SetLevel` menaikkan/menurunkan verbosity tanpa restart; semua logger turunan (`With`/`WithGroup`) berbagi level yang sama.
- **Async writer** — `slogx.AsyncWriter` menampan log di buffer dan menulis ke sink di goroutine latar belakang, mengurangi overhead syscall I/O.
- **Redaksi bertingkat** — kunci sensitif daftar tunggal (`SensitiveKeys`), atau redaksi path-based ala Pino (`RedactConfig`) dengan dot-notation, wildcard, dan opsi menghapus kunci sepenuhnya.
- **Wide events / canonical log line** — kumpulkan konteks kaya dalam satu event selama request berjalan, tulis sekali di akhir.
- **Tail sampling** — putusan "simpan atau buang" diambil setelah request selesai berdasarkan hasilnya (error, latensi, status code), bukan diambil acak di awal.
- **Aman secara konstruksi** — `secure.SafeError` memisahkan pesan publik dari detail internal; `secure.Secret[T]` meredaksi nilai otomatis di level tipe.
- **Murni pustaka standar** — `slog.Handler`, `slog.LogValuer`, dan interface kecil lain; modul utama tanpa dependensi pihak ketiga.

## Struktur paket

```
logkit/
├── logkit.go, levels.go   # Logger core + level Trace/Fatal
├── logctx/                # Propagasi request/correlation ID lewat konteks
├── event/                 # Wide event / canonical log line builder
├── secure/                # SafeError, Secret[T], Redactor
├── sampler/               # Tail sampling
├── slogx/                 # Handler JSON/Console, MultiHandler, AsyncWriter, redaksi
└── middleware/
    ├── httpmw/            # Middleware HTTP (mux standar, chi, gorilla, dst.)
    └── grpcmw/            # Interceptor gRPC (modul Go terpisah)
```

## Instalasi

```bash
go get github.com/semmidev/logkit

# gRPC (modul terpisah, hanya jika dipakai)
go get github.com/semmidev/logkit/middleware/grpcmw
```

## Memulai

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/slogx"
)

func main() {
	aw := slogx.NewAsyncWriter(os.Stdout, 8192, 100*time.Millisecond)
	defer aw.Close()

	logger := logkit.New(logkit.Config{
		Format:      logkit.FormatJSON, // FormatConsole untuk development
		Writer:      aw,
		Level:       slog.LevelInfo,
		ServiceName: "user-service",
		Version:     "1.4.2",
		Environment: "production",
		Redact: &logkit.RedactConfig{
			Paths: []string{
				"password",                  // kapan pun level key-nya muncul
				"req.headers.authorization", // path group spesifik
				"*.password",                // password di kedalaman mana pun
			},
			Censor: "[REDACTED]",
		},
	})
	defer logger.Sync()

	logger.Info(context.Background(), "service started", "port", 8080)

	if err := serve(); err != nil {
		logger.Fatal(context.Background(), "service stopped", "error", err) // log, lalu os.Exit(1)
	}
}
```

Level tambahan: `logger.Trace(ctx, ...)` dan `logger.Fatal(ctx, ...)` di samping Debug/Info/Warn/Error bawaan slog.

## Propagasi konteks

ID di-stamp sekali di middleware, semua logger yang dipanggil di bawahnya otomatis mewarisi:

```go
ctx = logctx.WithRequestID(ctx, logctx.NewID())
ctx = logctx.WithCorrelationID(ctx, logctx.NewID())
ctx = logctx.WithFields(ctx)

// di sub-function, tanpa pegangan logger:
logctx.Enrich(ctx, "user_id", "u_999", "tier", "premium")

logger.Info(ctx, "user purchased item")
// langsung memuat request_id, correlation_id, user_id, tier
```

`Enrich` pada konteks tanpa fields bag adalah no-op, jadi aman dipanggil dari kode library yang tidak mengetahui logkit.

## Wide event

Satu event kaya per request, ditulis sekali di akhir:

```go
ev := event.New()
ctx = event.WithEvent(ctx, ev)

// di handler mana pun sepanjang request:
event.Enrich(ctx, "order.id", orderID)
event.Enrich(ctx, "payment.attempt", attempt) // tulis terakhir menang

// di akhir request:
ev.SetError(err) // nil menetapkan hasil sukses
if decide := sampler.NewTailSampler(500, 0.05); decide.ShouldSample(ev.Snapshot()) {
	logger.Info(ctx, "request completed", ev.Args()...)
}
```

## Tail sampling

Keputusan diambil setelah request selesai, sehingga request yang bermasalah tidak hilang:

```go
s := sampler.NewTailSampler(500 /* ms */, 0.05)
// aturan default: selalu simpan error, status >= 500, dan request lambat;
// 5% dari sisanya disimpan acak sebagai baseline.

// tambah aturan baru tanpa menulis ulang sampler:
s.Rules = append(s.Rules, sampler.RuleFieldEquals("tenant", "qa-internal"))
```

Implementasi kustom cukup dengan `sampler.Decider` atau `sampler.DeciderFunc`.

## SafeError dan Secret

Detail error internal tidak pernah sampai ke pesan publik, tapi tetap ada di log server:

```go
// di lapisan database:
return secure.Wrap(secure.CodeUnavailable, "Unable to retrieve user profile.", err)
// err.Error() = pesan aman; err.Cause() = detail asli untuk log

// di batas publik (HTTP handler):
httpmw.RespondError(ctx, w, logger, err)
// log detail lengkap + tulis hanya kode + pesan aman ke klien

// di panggilan log:
logger.Error(ctx, "operation failed", "error", err)
// *SafeError diperluas otomatis via slog.LogValuer: kode, pesan, penyebab, metadata

// redaksi level tipe:
type Config struct {
	DBPassword secure.Secret[string]
}
logger.Info(ctx, "config loaded", "config", cfg) // ***REDACTED***
pw := cfg.DBPassword.Reveal() // satu-satunya titik akses nilai mentah
```

## Caller skip untuk helper

Saat membungkus logger di helper atau middleware, arahkan `file:line` ke pemanggil:

```go
func logTransactionError(ctx context.Context, logger *logkit.Logger, txID string, err error) {
	logger.WithCallerSkip(1).Error(ctx, "transaction failed", "tx_id", txID, "error", err)
}
```

## Middleware

Satu baris log kanonis per request, termasuk request ID propagation, panic recovery, dan sampling:

```go
mw := httpmw.Middleware(httpmw.Config{
	Logger:  logger,
	Sampler: sampler.NewTailSampler(500, 0.05), // default: sampler.Always
})
handler := mw(mux)
```

Untuk gRPC gunakan modul terpisah `grpcmw` (`UnaryServerInterceptor`, `StreamServerInterceptor`), dengan opsi konfigurasi yang setara.

Level hasil mengikuti kode status: 5xx/error menjadi Error, 4xx ke Warn, sisanya Info.

## Level dinamis di runtime

```go
logger.SetLevel(slog.LevelDebug)      // satu sumber, semua turunan ikut
logger.Leveler().Set(slog.LevelTrace) // atau atur *slog.LevelVar langsung,
// berguna di endpoint admin/HUP untuk mengatur verbosity tanpa restart.
```

Baca level dari env/config: `logkit.ParseLevel("trace")`.

## Escape hatch

`Config.Handler` menerima `slog.Handler` apa pun (bawaan, pihak ketiga, milik sendiri) sebagai ganti handler yang dibangun dari Format/Writer. Semua fitur di atas tetap berjalan di atasnya. Untuk menggabungkan beberapa tujuan gunakan `slogx.NewMultiHandler(jsonHandler, consoleHandler)`.

| Tujuan | Metode |
|---|---|
| Kirim log ke Loki/Datadog sink | `slog.Handler` kustom → `Config.Handler` |
| Ganti algoritma generator ID | callback `GenerateID` di config middleware |
| Sampling kustom | implementasi `sampler.Decider` |
| Meredaksi/mengubah atribut sebelum ditulis | `slogx.HandlerOptions.ReplaceAttr` |

## Pengujian dan contoh

```bash
go test ./...              # dengan -race untuk tes concurrency
go test -bench=. -benchmem # benchmark
go run ./examples/basicapp
go run ./examples/httpapp
```

## Lisensi

Lihat [LICENSE](LICENSE).
