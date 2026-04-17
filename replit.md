# ETH Wallet Tool

Proyek CLI berbasis **Go** untuk generate, check balance, dan hunting wallet Ethereum secara massal dan cepat.

## Struktur Proyek

```
eth-wallet-tool/
├── bin/
│   ├── eth-generator     # Binary: generator dompet massal
│   ├── eth-checker       # Binary: cek balance satu / banyak alamat
│   └── eth-hunt          # Binary: generate + check otomatis (hunting)
├── cmd/
│   ├── generator/main.go
│   ├── checker/main.go
│   └── hunt/main.go
├── internal/
│   ├── proxy/proxy.go    # Proxy manager (fetch, validasi, round-robin)
│   └── wallet/wallet.go  # Ethereum wallet generator
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## File Akses Cepat (Root)

File berikut tersedia langsung di folder root (symlink ke `eth-wallet-tool/`):
- `config.yaml` → Edit konfigurasi chain, RPC, proxy, Telegram, dll.
- `found.txt` → Hasil wallet yang ditemukan dengan saldo

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

## Penggunaan

### eth-generator — Generate wallet massal

```bash
./bin/eth-generator -n 1000 -workers 8 -o wallets.txt
./bin/eth-generator -n 500 -csv -o wallets.csv
```

### eth-checker — Cek balance alamat

```bash
./bin/eth-checker -addr 0xABC...
./bin/eth-checker -file addresses.txt -workers 20 -o hasil.txt
./bin/eth-checker -proxy -fetch-proxy -addr 0xABC...
```

### eth-hunt — Auto generate + check

```bash
./bin/eth-hunt -workers 16
./bin/eth-hunt -workers 32 -proxy -fetch-proxy -o found.txt
./bin/eth-hunt -workers 8 -rpc "https://eth.llamarpc.com,https://ethereum.publicnode.com"
```

## Default RPC Endpoints

1. https://eth.llamarpc.com
2. https://ethereum.publicnode.com
3. https://eth-mainnet.public.blastapi.io
4. https://rpc.payload.de
5. https://1rpc.io/eth

## Fitur Utama

- Concurrent worker pool dengan round-robin RPC
- Multi-RPC failover + retry otomatis
- Auto-fetch proxy dari 7 sumber GitHub
- Validasi proxy via Ethereum RPC test
- Auto-remove proxy mati (setelah 3 gagal)
- Auto-refetch ketika pool proxy habis
- Background refresh proxy setiap 2 menit
- Shutdown instan (context propagation)
- Output colorized terminal

## Go Version

Go 1.25.5
