# ETH Wallet Tool

Tool CLI berbasis Go untuk generate, cek balance, dan hunting wallet Ethereum secara massal dan cepat.

---

## Fitur

- **Generate** hingga 10.000+ wallet/detik
- **Cek balance** banyak alamat sekaligus dengan worker pool
- **Auto hunt** — generate + cek otomatis tanpa henti
- Multi-RPC round-robin dengan retry otomatis
- Dukungan proxy HTTP dengan auto-fetch dari 7 sumber GitHub
- Validasi proxy via Ethereum RPC test
- Auto-hapus proxy mati, auto-refetch saat pool habis
- Shutdown instan saat Ctrl+C

---

## Cara Pakai Cepat

```bash
# Generate 10 wallet (default)
./eth-generator

# Cek balance satu alamat
./eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Mulai hunting (generate + cek otomatis)
./eth-hunt
```

---

## eth-generator — Generate Wallet

```bash
./eth-generator [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-n` | `10` | Jumlah wallet yang digenerate |
| `-workers` | jumlah CPU | Jumlah worker concurrent |
| `-o` | stdout | Simpan ke file |
| `-priv` | `true` | Tampilkan private key |
| `-pub` | `false` | Tampilkan public key |
| `-csv` | `false` | Output format CSV |

**Contoh:**

```bash
# Generate 1000 wallet ke file
./eth-generator -n 1000 -o wallets.txt

# Generate 5000 wallet format CSV dengan 16 worker
./eth-generator -n 5000 -workers 16 -csv -o wallets.csv

# Hanya tampilkan address (tanpa private key)
./eth-generator -n 100 -priv=false
```

---

## eth-checker — Cek Balance

```bash
./eth-checker [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-addr` | — | Satu alamat untuk dicek |
| `-file` | — | File berisi daftar alamat (1 per baris) |
| `-workers` | 3×CPU | Jumlah worker concurrent |
| `-rpc` | 5 RPC publik | Comma-separated RPC URLs |
| `-timeout` | `15s` | Timeout per request |
| `-retries` | `4` | Retry jika gagal |
| `-delay` | `200ms` | Delay antar retry |
| `-proxy` | `false` | Gunakan proxy |
| `-pfile` | `proxies.txt` | File proxy |
| `-fetch-proxy` | `false` | Fetch & validasi proxy baru |
| `-proxy-workers` | `50` | Worker untuk validasi proxy |
| `-o` | — | Simpan hasil ke file |
| `-min` | `0` | Filter minimum balance (ETH) |

**Contoh:**

```bash
# Cek satu alamat
./eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Cek banyak alamat dari file
./eth-checker -file addresses.txt -workers 50 -o hasil.txt

# Cek dengan proxy (fetch proxy dulu)
./eth-checker -file addresses.txt -proxy -fetch-proxy

# Hanya tampilkan yang punya balance > 0.1 ETH
./eth-checker -file addresses.txt -min 0.1

# Gunakan RPC sendiri
./eth-checker -addr 0xABC... -rpc "https://mainnet.infura.io/v3/KEY"
```

---

## eth-hunt — Auto Hunting

Generate wallet baru secara terus-menerus lalu langsung cek balancenya. Jika ditemukan wallet dengan balance, otomatis disimpan ke file.

```bash
./eth-hunt [opsi]
```

| Flag | Default | Keterangan |
|------|---------|------------|
| `-workers` | 3×CPU | Jumlah worker concurrent |
| `-rpc` | 5 RPC publik | Comma-separated RPC URLs |
| `-timeout` | `12s` | Timeout per request |
| `-retries` | `3` | Retry jika gagal |
| `-proxy` | `false` | Gunakan proxy |
| `-pfile` | `proxies.txt` | File proxy |
| `-fetch-proxy` | `false` | Fetch & validasi proxy baru sekarang |
| `-proxy-workers` | `50` | Worker validasi proxy |
| `-o` | `found.txt` | File output wallet yang punya balance |
| `-stats` | `5s` | Interval tampilkan statistik |

**Contoh:**

```bash
# Hunt dasar
./eth-hunt

# Hunt dengan 32 worker
./eth-hunt -workers 32

# Hunt dengan proxy (auto-fetch dari internet)
./eth-hunt -workers 16 -proxy -fetch-proxy

# Hunt dengan statistik lebih sering + output custom
./eth-hunt -workers 24 -stats 2s -o temuan.txt

# Hunt dengan RPC sendiri
./eth-hunt -workers 16 -rpc "https://mainnet.infura.io/v3/KEY,https://eth.llamarpc.com"
```

**Hentikan:** tekan `Ctrl+C` — akan langsung berhenti dan tampilkan ringkasan.

---

## Proxy

Tool ini bisa menggunakan proxy HTTP gratis dari internet untuk menghindari rate-limit RPC.

```bash
# Fetch proxy baru + langsung validasi saat hunt
./eth-hunt -proxy -fetch-proxy

# Fetch proxy untuk checker
./eth-checker -file addr.txt -proxy -fetch-proxy
```

Proxy yang valid disimpan ke `proxies.txt`. Proxy mati otomatis dibuang setelah 3 kali gagal. Jika pool habis, otomatis fetch ulang dari sumber.

**Sumber proxy (7 GitHub):**
- TheSpeedX/PROXY-List
- clarketm/proxy-list
- ShiftyTR/Proxy-List
- monosans/proxy-list
- jetkai/proxy-list
- mertguvencli/http-proxy-list
- roosterkid/openproxylist

---

## Default RPC Endpoints

| # | URL |
|---|-----|
| 1 | https://eth.llamarpc.com |
| 2 | https://ethereum.publicnode.com |
| 3 | https://eth-mainnet.public.blastapi.io |
| 4 | https://rpc.payload.de |
| 5 | https://1rpc.io/eth |

---

## Build dari Source

Butuh **Go 1.21+**

```bash
cd eth-wallet-tool

# Build semua
go build -ldflags="-s -w" -o bin/eth-generator ./cmd/generator/
go build -ldflags="-s -w" -o bin/eth-checker  ./cmd/checker/
go build -ldflags="-s -w" -o bin/eth-hunt     ./cmd/hunt/

# Atau pakai Makefile
make build
```

---

## Output Format

### found.txt (hasil hunt / checker)
```
ADDRESS=0xABC... | PRIVKEY=0x123... | BALANCE=1.23456789 ETH
```

### wallets.txt (generator)
```
[#0001] Address: 0xABC... | PrivKey: 0x123...
```

### wallets.csv (generator -csv)
```
index,address,private_key
1,0xABC...,123...
```

---

## Struktur Proyek

```
eth-wallet-tool/
├── bin/
│   ├── eth-generator
│   ├── eth-checker
│   └── eth-hunt
├── cmd/
│   ├── generator/main.go
│   ├── checker/main.go
│   └── hunt/main.go
├── internal/
│   ├── proxy/proxy.go
│   └── wallet/wallet.go
├── go.mod
└── Makefile
```
