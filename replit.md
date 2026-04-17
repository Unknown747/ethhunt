# ETH Wallet Tool

Proyek CLI berbasis **Go 1.25.5** untuk generate, cek balance, dan hunting wallet Ethereum secara massal dan cepat. Mendukung multi-chain, batch RPC, proxy otomatis, notifikasi Telegram, BIP39 mnemonic, dan token ERC-20.

## Struktur Proyek

```
/                           ← Root (akses cepat)
├── eth-generator           # Shell wrapper → eth-wallet-tool/bin/eth-generator
├── eth-checker             # Shell wrapper → eth-wallet-tool/bin/eth-checker
├── eth-hunt                # Shell wrapper → eth-wallet-tool/bin/eth-hunt
├── config.yaml             # Symlink → eth-wallet-tool/config.yaml
├── found.txt               # Symlink → eth-wallet-tool/found.txt
└── eth-wallet-tool/
    ├── bin/
    │   ├── eth-generator   # Binary: generator wallet massal
    │   ├── eth-checker     # Binary: cek balance satu/banyak address
    │   └── eth-hunt        # Binary: generate + check otomatis (hunting)
    ├── cmd/
    │   ├── generator/main.go
    │   ├── checker/main.go
    │   └── hunt/main.go
    ├── internal/
    │   ├── config/config.go     # YAML config loader
    │   ├── rpc/manager.go       # Batch RPC + dead endpoint detection
    │   ├── notify/telegram.go   # Notifikasi Telegram
    │   ├── mnemonic/mnemonic.go # BIP39/BIP44 derivation
    │   ├── proxy/proxy.go       # Proxy manager (fetch, validasi, round-robin)
    │   └── wallet/wallet.go     # Random wallet generator (ECDSA)
    ├── config.yaml         # ← Edit ini untuk konfigurasi
    ├── go.mod              # Go 1.25.5 + toolchain go1.25.5
    └── Makefile
```

## Cara Pakai (dari Root)

```bash
# Generate 10 wallet
./eth-generator

# Cek balance satu address
./eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Mulai hunting (tekan Ctrl+C untuk berhenti)
./eth-hunt
```

## Build

Gunakan Go 1.25.5 dengan `GOTOOLCHAIN=local` untuk menghindari error "toolchain not available":

```bash
cd eth-wallet-tool
export GOTOOLCHAIN=local
export PATH=/nix/store/60z37432vmgkg54krwr1z057bqwp7583-go-1.25.5/bin:$PATH
go build -ldflags="-s -w" -o bin/eth-generator ./cmd/generator/
go build -ldflags="-s -w" -o bin/eth-checker  ./cmd/checker/
go build -ldflags="-s -w" -o bin/eth-hunt     ./cmd/hunt/
```

Atau via Makefile (dari dalam `eth-wallet-tool/`):
```bash
make build
```

## File Penting

| File | Keterangan |
|------|------------|
| `config.yaml` | Konfigurasi chain, RPC, proxy, Telegram, dll |
| `found.txt` | Wallet dengan balance yang ditemukan |
| `stats.csv` | Log statistik performa (auto-generated, tidak dicommit) |
| `.hunt_resume` | State resume sesi hunt (auto-generated, tidak dicommit) |
| `proxies.txt` | Proxy aktif (auto-generated, tidak dicommit) |

## Go Version

Go 1.25.5 — satu-satunya versi yang terpasang (go-1.21 sudah dihapus).
Gunakan `GOTOOLCHAIN=local` saat build untuk mencegah download toolchain.

## Fitur Utama

- Concurrent worker pool dengan round-robin RPC
- Batch RPC (20 address per call) untuk efisiensi tinggi
- Multi-chain: Ethereum, BSC, Polygon, Arbitrum
- Multi-RPC failover + dead endpoint detection
- Auto-fetch proxy dari 7 sumber GitHub
- BIP39 mnemonic (12/24 kata)
- Token ERC-20: USDT, USDC
- Notifikasi Telegram saat wallet funded ditemukan
- Stats CSV log & resume sesi
