# ETH Wallet Tool (Go)

High-speed Ethereum wallet generator, balance checker, dan auto-hunter dengan proxy support.

## Binary yang tersedia

| Binary | Fungsi |
|--------|--------|
| `bin/eth-generator` | Generate wallet ETH baru secara massal |
| `bin/eth-checker` | Cek balance dari daftar address |
| `bin/eth-hunt` | **Auto generate + check sekaligus + proxy** |

---

## Build

```bash
# Build semua sekaligus
make build

# Atau manual satu per satu
go build -ldflags="-s -w" -o bin/eth-generator ./cmd/generator/
go build -ldflags="-s -w" -o bin/eth-checker  ./cmd/checker/
go build -ldflags="-s -w" -o bin/eth-hunt     ./cmd/hunt/
```

---

## eth-hunt — Auto Generate + Check (Rekomendasi)

Tool utama yang generate wallet baru dan langsung cek balance secara otomatis.
Wallet yang punya balance langsung disimpan ke `found.txt`.

```bash
# Mulai hunting (tekan Ctrl+C untuk berhenti)
./bin/eth-hunt

# Lebih banyak worker = lebih cepat
./bin/eth-hunt -workers 16

# Gunakan RPC sendiri (lebih cepat & stabil)
./bin/eth-hunt -workers 32 -rpc "https://mainnet.infura.io/v3/KEY"

# Dengan proxy (auto-fetch dari internet)
./bin/eth-hunt -proxy -fetch-proxy

# Dengan proxy dari file yang sudah ada
./bin/eth-hunt -proxy

# Simpan ke file custom
./bin/eth-hunt -o hasil_hunt.txt -stats 10s
```

**Flag eth-hunt:**
| Flag | Default | Deskripsi |
|------|---------|-----------|
| `-workers` | CPU*3 | Jumlah goroutine concurrent |
| `-rpc` | 5 public RPCs | Comma-separated RPC URLs |
| `-timeout` | 12s | Timeout per request |
| `-retries` | 3 | Retry per wallet |
| `-o` | found.txt | File output wallet funded |
| `-stats` | 5s | Interval tampilkan statistik |
| `-proxy` | false | Aktifkan proxy |
| `-pfile` | proxies.txt | Path file proxy |
| `-fetch-proxy` | false | Fetch & validasi proxy baru dari internet |
| `-proxy-workers` | 50 | Goroutine untuk validasi proxy |

---

## eth-generator — Bulk Wallet Generator

```bash
# Generate 1000 wallet
./bin/eth-generator -n 1000

# Dengan 8 worker, simpan ke file
./bin/eth-generator -n 10000 -workers 8 -o wallets.txt

# Format CSV
./bin/eth-generator -n 5000 -csv -o wallets.csv

# Tampilkan public key juga
./bin/eth-generator -n 10 -pub

# Benchmark 100k wallet
./bin/eth-generator -n 100000 -workers 16 -o /dev/null
```

**Flag eth-generator:**
| Flag | Default | Deskripsi |
|------|---------|-----------|
| `-n` | 10 | Jumlah wallet |
| `-workers` | CPU count | Jumlah goroutine |
| `-o` | stdout | Output file |
| `-priv` | true | Tampilkan private key |
| `-pub` | false | Tampilkan public key |
| `-csv` | false | Format CSV |

---

## eth-checker — Balance Checker dari File

```bash
# Cek satu address
./bin/eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Cek dari file (satu address per baris, atau CSV)
./bin/eth-checker -f addresses.txt

# Hanya tampilkan yang punya balance
./bin/eth-checker -f addresses.txt -funded

# Gunakan proxy
./bin/eth-checker -f addresses.txt -proxy -fetch-proxy

# Custom RPC
./bin/eth-checker -f addresses.txt -rpc "https://mainnet.infura.io/v3/KEY" -workers 50

# Simpan hasil ke file
./bin/eth-checker -f addresses.txt -funded -o funded.txt

# Filter minimum balance
./bin/eth-checker -f addresses.txt -min 0.1 -funded
```

**Flag eth-checker:**
| Flag | Default | Deskripsi |
|------|---------|-----------|
| `-f` | - | File daftar address |
| `-addr` | - | Single address |
| `-rpc` | 5 public RPCs | RPC URLs |
| `-workers` | CPU*2 | Goroutine concurrent |
| `-timeout` | 15s | Timeout per request |
| `-retries` | 4 | Retry per address |
| `-delay` | 200ms | Delay antar retry |
| `-funded` | false | Hanya tampilkan yang punya balance |
| `-o` | - | Output file |
| `-min` | 0 | Minimum ETH balance |
| `-proxy` | false | Aktifkan proxy |
| `-pfile` | proxies.txt | File proxy |
| `-fetch-proxy` | false | Fetch proxy baru |
| `-proxy-workers` | 50 | Worker validasi proxy |

---

## Sistem Proxy

Proxy diambil otomatis dari 7 sumber GitHub proxy list publik, divalidasi, dan disimpan ke `proxies.txt`.

### Cara kerja:
1. `-fetch-proxy` → ambil dari internet, validasi setiap proxy, simpan yang aktif ke `proxies.txt`
2. Saat hunting/checking, proxy dipakai secara round-robin
3. Proxy yang gagal 3x otomatis dibuang dari pool
4. Jika pool proxy habis → auto-fetch ulang dari internet
5. Setiap 2 menit → cek apakah perlu refetch

```bash
# Fetch proxy tanpa langsung hunt (jalankan dulu, baru hunt)
./bin/eth-hunt -fetch-proxy -workers 2 &   # biarkan jalan background
sleep 60 && kill %1                        # tunggu 60 detik lalu stop
# Sekarang proxies.txt sudah terisi
./bin/eth-hunt -proxy -workers 16          # hunt pakai proxy yang sudah ada
```

### Sumber proxy:
- TheSpeedX PROXY-List (HTTP)
- clarketm proxy-list
- ShiftyTR Proxy-List
- monosans proxy-list
- jetkai proxy-list
- mertguvencli http-proxy-list
- roosterkid openproxylist

---

## Format Input File Checker

```
# Komentar diawali #
0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# CSV (index,address,privkey)
1,0xAddress1,privkey1

# Format address:privkey
0xAddress1:privkey1
```

---

## Tips Performa

| Mode | Kecepatan |
|------|-----------|
| Generator saja | ~10.000–100.000 wallet/detik |
| Hunt (free RPC) | ~10–30 wallet/detik |
| Hunt (RPC berbayar) | ~100–1000 wallet/detik |
| Hunt + banyak proxy | Bisa lebih tinggi lagi |

**RPC berbayar yang direkomendasikan:**
- [Infura](https://infura.io) — `https://mainnet.infura.io/v3/YOUR_KEY`
- [Alchemy](https://alchemy.com) — `https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY`
- [QuickNode](https://quicknode.com) — dari dashboard QuickNode
- Self-hosted full node — tercepat & tanpa limit
