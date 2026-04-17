# ETH Wallet Tool (Go)

High-speed Ethereum wallet generator dan balance checker menggunakan Go dengan concurrency penuh.

## Fitur

### Generator (`eth-generator`)
- Generate ribuan wallet ETH per detik
- Multi-goroutine worker pool
- Output ke stdout atau file
- Format normal atau CSV
- Menampilkan address, private key, public key

### Checker (`eth-checker`)
- Cek balance ETH secara concurrent (ratusan address/detik dengan RPC bagus)
- Round-robin load balancing ke multiple RPC endpoint
- Auto-retry dengan ganti RPC saat rate-limit (429)
- Support input: address tunggal, file teks, atau CSV
- Filter hanya address yang punya balance
- Simpan hasil ke file

## Build

```bash
# Build semua
make build

# Atau manual
go build -ldflags="-s -w" -o bin/eth-generator ./cmd/generator/
go build -ldflags="-s -w" -o bin/eth-checker ./cmd/checker/
```

## Penggunaan

### Generator

```bash
# Generate 10 wallet (default)
./bin/eth-generator

# Generate 1000 wallet dengan 8 worker
./bin/eth-generator -n 1000 -workers 8

# Generate ke file
./bin/eth-generator -n 5000 -o wallets.txt

# Format CSV (mudah diimport spreadsheet)
./bin/eth-generator -n 1000 -csv -o wallets.csv

# Tampilkan public key juga
./bin/eth-generator -n 10 -pub

# Benchmark 10000 wallet
./bin/eth-generator -n 10000 -workers 8 -o /dev/null
```

**Flag:**
| Flag | Default | Deskripsi |
|------|---------|-----------|
| `-n` | 10 | Jumlah wallet yang digenerate |
| `-workers` | CPU count | Jumlah goroutine concurrent |
| `-o` | stdout | Output file |
| `-priv` | true | Tampilkan private key |
| `-pub` | false | Tampilkan public key |
| `-csv` | false | Output format CSV |

### Checker

```bash
# Cek satu address
./bin/eth-checker -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Cek dari file (satu address per baris)
./bin/eth-checker -f addresses.txt

# Cek dari CSV (format: index,address,privkey atau address:privkey)
./bin/eth-checker -f wallets.csv

# Hanya tampilkan yang punya balance
./bin/eth-checker -f addresses.txt -funded

# Custom RPC (gunakan sendiri untuk lebih cepat & stabil)
./bin/eth-checker -addr 0x... -rpc "https://mainnet.infura.io/v3/YOUR_KEY"

# Multi-RPC untuk load balancing
./bin/eth-checker -f addresses.txt -rpc "https://rpc1.com,https://rpc2.com,https://rpc3.com"

# Simpan hasil funded ke file
./bin/eth-checker -f addresses.txt -funded -o funded.txt

# Atur concurrency & timeout
./bin/eth-checker -f addresses.txt -workers 20 -timeout 10s -retries 3

# Filter minimum balance (contoh: hanya > 0.1 ETH)
./bin/eth-checker -f addresses.txt -min 0.1 -funded
```

**Flag:**
| Flag | Default | Deskripsi |
|------|---------|-----------|
| `-f` | - | File berisi daftar address |
| `-addr` | - | Single address untuk dicek |
| `-rpc` | 5 public RPCs | Comma-separated RPC URLs |
| `-workers` | CPU*2 | Jumlah goroutine concurrent |
| `-timeout` | 15s | Timeout per request RPC |
| `-retries` | 4 | Retry count (ganti RPC tiap retry) |
| `-delay` | 200ms | Delay antar retry |
| `-funded` | false | Hanya tampilkan yang punya balance |
| `-o` | - | File output untuk funded address |
| `-min` | 0 | Minimum ETH balance untuk ditampilkan |

## Tips Performa

### Untuk kecepatan maksimal, gunakan RPC berbayar:
- **Infura**: `https://mainnet.infura.io/v3/YOUR_PROJECT_ID`
- **Alchemy**: `https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY`
- **QuickNode**: dari dashboard QuickNode
- **Self-hosted**: node ETH sendiri (paling cepat)

### Rekomendasi setting:
```bash
# Dengan RPC berbayar bisa gunakan banyak workers
./bin/eth-checker -f big_list.txt -rpc "https://mainnet.infura.io/v3/KEY" -workers 50 -funded

# Generator 100k wallet cepat
./bin/eth-generator -n 100000 -workers 16 -csv -o 100k_wallets.csv
```

## Format File Input (Checker)

File address mendukung berbagai format:
```
# Komentar diawali #
0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
0xAbCd...

# Format CSV juga didukung:
1,0xAddress1,privkey1
2,0xAddress2,privkey2

# Format address:privkey:
0xAddress1:privkey1
```

## RPC Publik Gratis (built-in)

| Endpoint | Keterangan |
|----------|------------|
| `https://eth.llamarpc.com` | LlamaNodes, stabil |
| `https://cloudflare-eth.com` | Cloudflare |
| `https://ethereum.publicnode.com` | PublicNode |
| `https://rpc.payload.de` | Payload |
| `https://eth-mainnet.public.blastapi.io` | Blast API |

> RPC gratis memiliki rate limit. Untuk penggunaan besar, gunakan RPC berbayar.
