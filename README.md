# ETH Wallet Tool

Tool CLI berbasis **Go** untuk generate, cek balance, dan hunting wallet Ethereum secara massal dan cepat. Mendukung multi-chain, batch RPC, proxy otomatis, notifikasi Telegram, BIP39 mnemonic, dan token ERC-20.

---

## Fitur Lengkap

| Fitur | Keterangan |
|-------|------------|
| ⚡ Generate cepat | Hingga 10.000+ random wallet/detik |
| 📦 Batch RPC | 20 address per HTTP call (jauh lebih efisien) |
| 🔄 Multi-chain | Ethereum, BSC, Polygon, Arbitrum |
| 💀 Dead RPC detection | RPC mati otomatis diistirahatkan & dicoba lagi |
| 🌐 Proxy otomatis | Fetch dari 7 sumber GitHub, validasi, round-robin |
| 🌱 BIP39 Mnemonic | Generate dari seed phrase 12/24 kata |
| 🪙 Token ERC-20 | Cek USDT, USDC, dll selain ETH native |
| 📬 Telegram notif | Alert otomatis saat wallet dengan balance ditemukan |
| 📊 Stats CSV log | Rekam performa tiap sesi ke file CSV |
| 🔁 Resume sesi | Akumulasi statistik lintas sesi hunt |
| 📈 Progress bar | Live progress saat generate & checker |
| ⚙️ Config YAML | Semua setting di satu file `config.yaml` |

---

## Cara Pakai Cepat

```bash
# Generate 10 wallet
./eth-generator

# Cek balance satu alamat
./eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Mulai hunting (tanpa batas)
./eth-hunt
```

---

## Config (config.yaml)

Semua pengaturan ada di `eth-wallet-tool/config.yaml`. Edit sesuai kebutuhan — **tidak perlu rebuild** setelah mengubah config.

```yaml
chain: ethereum   # ethereum | bsc | polygon | arbitrum

workers:
  hunt: 0         # 0 = auto (3 × CPU)

rpc:
  batch_size: 20  # Alamat per batch call
  timeout: 12s
  dead_threshold: 5  # Gagal sebelum RPC diistirahatkan

telegram:
  enabled: false
  token: ""       # Token dari @BotFather
  chat_id: ""     # Chat ID tujuan notifikasi

generator:
  mode: random    # random | mnemonic
  mnemonic_words: 12

tokens:
  check_erc20: false   # Aktifkan untuk cek USDT/USDC

output:
  found_file: found.txt
  stats_log: stats.csv
```

Lihat `eth-wallet-tool/config.yaml` untuk semua opsi lengkap.

---

## eth-generator — Generate Wallet

```bash
./eth-generator [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-n` | `10` | Jumlah wallet |
| `-workers` | auto | Worker concurrent |
| `-mode` | dari config | `random` atau `mnemonic` |
| `-mnem` | `false` | Tampilkan mnemonic (mode mnemonic) |
| `-o` | stdout | Simpan ke file |
| `-priv` | `true` | Tampilkan private key |
| `-pub` | `false` | Tampilkan public key |
| `-csv` | `false` | Output CSV |
| `-config` | `config.yaml` | Path file config |

**Contoh:**

```bash
# Generate 1000 wallet random
./eth-generator -n 1000 -o wallets.txt

# Generate 100 wallet dari BIP39 mnemonic (tampilkan seed phrase)
./eth-generator -n 100 -mode mnemonic -mnem

# Generate format CSV
./eth-generator -n 500 -csv -o wallets.csv

# 24-kata mnemonic (ubah di config.yaml: mnemonic_words: 24)
./eth-generator -n 10 -mode mnemonic -mnem
```

---

## eth-checker — Cek Balance

```bash
./eth-checker [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-addr` | — | Satu alamat |
| `-f` | — | File daftar alamat |
| `-chain` | dari config | `ethereum\|bsc\|polygon\|arbitrum` |
| `-workers` | auto | Worker concurrent |
| `-rpc` | dari config | Comma-separated RPC URLs |
| `-funded` | `false` | Hanya tampilkan yang punya balance |
| `-min` | `0` | Filter minimum balance |
| `-o` | — | Simpan hasil ke file |
| `-proxy` | `false` | Gunakan proxy |
| `-fetch-proxy` | `false` | Fetch proxy baru |
| `-config` | `config.yaml` | Path file config |

**Contoh:**

```bash
# Cek satu alamat (Ethereum)
./eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Cek banyak alamat dari file
./eth-checker -f addresses.txt -workers 50 -o hasil.txt

# Cek di BSC
./eth-checker -f addresses.txt -chain bsc

# Cek dengan proxy
./eth-checker -f addresses.txt -proxy -fetch-proxy

# Hanya tampilkan yang punya > 0.1 ETH
./eth-checker -f addresses.txt -funded -min 0.1

# Cek USDT/USDC juga (aktifkan check_erc20: true di config)
./eth-checker -addr 0xABC...
```

---

## eth-hunt — Auto Hunting

Generate wallet tanpa henti + cek balance secara paralel menggunakan batch RPC.

```bash
./eth-hunt [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-workers` | auto | Worker concurrent |
| `-chain` | dari config | Chain target |
| `-rpc` | dari config | RPC URLs custom |
| `-mode` | dari config | `random` atau `mnemonic` |
| `-proxy` | `false` | Gunakan proxy |
| `-fetch-proxy` | `false` | Fetch proxy baru sekarang |
| `-o` | dari config | File output |
| `-stats` | dari config | Interval statistik |
| `-test-telegram` | `false` | Test notif Telegram |
| `-config` | `config.yaml` | Path file config |

**Contoh:**

```bash
# Hunt dasar
./eth-hunt

# Hunt dengan banyak worker
./eth-hunt -workers 64

# Hunt di BSC
./eth-hunt -chain bsc

# Hunt dengan proxy otomatis
./eth-hunt -proxy -fetch-proxy

# Hunt dengan mnemonic BIP39
./eth-hunt -mode mnemonic

# Hunt + notif Telegram (atur config dulu)
./eth-hunt

# Test koneksi Telegram
./eth-hunt -test-telegram
```

**Hentikan:** tekan `Ctrl+C` — langsung berhenti, ringkasan ditampilkan, state disimpan.

---

## Setup Telegram

1. Buka [@BotFather](https://t.me/BotFather) di Telegram → `/newbot` → salin token
2. Kirim pesan ke bot kamu, lalu buka:
   ```
   https://api.telegram.org/bot<TOKEN>/getUpdates
   ```
   Cari `chat.id` di response JSON
3. Edit `eth-wallet-tool/config.yaml`:
   ```yaml
   telegram:
     enabled: true
     token: "7123456789:AAHdq..."
     chat_id: "123456789"
   ```
4. Test:
   ```bash
   ./eth-hunt -test-telegram
   ```

---

## Multi-Chain

Ganti chain via flag atau config:

```bash
./eth-hunt -chain bsc       # BNB Smart Chain
./eth-hunt -chain polygon   # Polygon
./eth-hunt -chain arbitrum  # Arbitrum One

./eth-checker -addr 0xABC... -chain bsc
```

Tambah chain baru di `config.yaml` bagian `chains:`.

---

## Token ERC-20

Aktifkan di `config.yaml`:
```yaml
tokens:
  check_erc20: true
  list:
    - name: USDT
      address: "0xdAC17F958D2ee523a2206206994597C13D831ec7"
      decimals: 6
    - name: USDC
      address: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
      decimals: 6
```

> Catatan: Token ERC-20 hanya berlaku untuk chain Ethereum. Dicek dalam satu batch call yang sama (tidak memperlambat).

---

## Proxy

```bash
# Fetch proxy baru + langsung pakai
./eth-hunt -proxy -fetch-proxy

# Pakai proxy yang sudah ada di proxies.txt
./eth-hunt -proxy
```

Proxy mati otomatis dibuang (setelah 3 gagal). Pool habis → auto-refetch setiap 2 menit.

**7 Sumber proxy:**
- TheSpeedX/PROXY-List
- clarketm/proxy-list
- ShiftyTR/Proxy-List
- monosans/proxy-list
- jetkai/proxy-list
- mertguvencli/http-proxy-list
- roosterkid/openproxylist

---

## Output Files

| File | Keterangan |
|------|------------|
| `found.txt` | Wallet dengan balance ditemukan |
| `stats.csv` | Log statistik performa tiap interval |
| `.hunt_resume` | State resume (akumulasi lintas sesi) |
| `proxies.txt` | Proxy aktif yang tersimpan |

**Format found.txt:**
```
ADDRESS=0xABC... | PRIVKEY=0x123... | BALANCE=1.23456789 ETH | USDT=100.000000
```

**Format stats.csv:**
```
timestamp,generated,checked,found,errors,gen_rate,check_rate,rpc_alive
2024-01-01T00:00:05Z,1800,1600,0,12,360.0,320.0,5
```

---

## Build dari Source

Butuh **Go 1.25.5**

```bash
cd eth-wallet-tool
export GOTOOLCHAIN=local

go build -ldflags="-s -w" -o bin/eth-generator ./cmd/generator/
go build -ldflags="-s -w" -o bin/eth-checker  ./cmd/checker/
go build -ldflags="-s -w" -o bin/eth-hunt     ./cmd/hunt/

# Atau via Makefile
make build
```

---

## Struktur Proyek

```
/                               ← Root (akses cepat)
├── eth-generator               # Wrapper → eth-wallet-tool/bin/eth-generator
├── eth-checker                 # Wrapper → eth-wallet-tool/bin/eth-checker
├── eth-hunt                    # Wrapper → eth-wallet-tool/bin/eth-hunt
├── config.yaml                 # Symlink → eth-wallet-tool/config.yaml
├── found.txt                   # Symlink → eth-wallet-tool/found.txt
└── eth-wallet-tool/
    ├── bin/
    │   ├── eth-generator
    │   ├── eth-checker
    │   └── eth-hunt
    ├── cmd/
    │   ├── generator/main.go   # Generator v2 (random + BIP39, progress bar)
    │   ├── checker/main.go     # Checker v3 (batch RPC, multi-chain, progress)
    │   └── hunt/main.go        # Hunter v3 (semua fitur terintegrasi)
    ├── internal/
    │   ├── config/config.go    # YAML config loader
    │   ├── rpc/manager.go      # Batch RPC + dead detection
    │   ├── notify/telegram.go  # Telegram bot notifications
    │   ├── mnemonic/mnemonic.go # BIP39/BIP44 wallet derivation
    │   ├── proxy/proxy.go      # Proxy manager
    │   └── wallet/wallet.go    # Random wallet generator
    ├── config.yaml             # ← Edit ini untuk konfigurasi
    ├── go.mod                  # Go 1.25.5
    └── Makefile
```
