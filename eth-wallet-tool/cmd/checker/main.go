package main

import (
        "bufio"
        "context"
        "flag"
        "fmt"
        "math/big"
        "net/http"
        "os"
        "runtime"
        "strings"
        "sync"
        "sync/atomic"
        "time"

        "github.com/ethereum/go-ethereum/common"
        "github.com/fatih/color"

        "eth-wallet-tool/internal/config"
        "eth-wallet-tool/internal/rpc"
        "eth-wallet-tool/internal/wallet"
)

var (
        boldCyan   = color.New(color.FgCyan, color.Bold)
        boldGreen  = color.New(color.FgGreen, color.Bold)
        boldYellow = color.New(color.FgYellow, color.Bold)
        boldRed    = color.New(color.FgRed, color.Bold)
        boldWhite  = color.New(color.FgWhite, color.Bold)
        dimWhite   = color.New(color.FgWhite, color.Faint)
)

func printBanner() {
        boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET CHECKER  v4.0                  ║
║    High-Speed Ethereum Balance Checker            ║
╚═══════════════════════════════════════════════════╝`)
}

func loadAddresses(filePath string) ([]string, error) {
        f, err := os.Open(filePath)
        if err != nil {
                return nil, fmt.Errorf("cannot open: %w", err)
        }
        defer f.Close()

        var addresses []string
        scanner := bufio.NewScanner(f)
        for scanner.Scan() {
                line := strings.TrimSpace(scanner.Text())
                if line == "" || strings.HasPrefix(line, "#") {
                        continue
                }
                parts := strings.FieldsFunc(line, func(r rune) bool { return r == ':' || r == ',' })
                for _, p := range parts {
                        p = strings.TrimSpace(p)
                        if wallet.IsValidAddress(p) {
                                addresses = append(addresses, common.HexToAddress(p).Hex())
                                break
                        }
                }
        }
        return addresses, scanner.Err()
}

func makeBatches(addresses []string, size int) [][]string {
        var batches [][]string
        for size < 1 {
                size = 20
        }
        for i := 0; i < len(addresses); i += size {
                end := i + size
                if end > len(addresses) {
                        end = len(addresses)
                }
                batches = append(batches, addresses[i:end])
        }
        return batches
}

type CheckResult struct {
        Address string
        ETHBal  *big.Float
        Tokens  map[string]*big.Float
        HasBal  bool
        Error   error
}

func main() {
        cfgFile := flag.String("config", "config.yaml", "path file konfigurasi")
        fileFlag := flag.String("f", "", "file berisi daftar address")
        addrFlag := flag.String("addr", "", "single address untuk dicek")
        rpcURLs := flag.String("rpc", "", "comma-separated RPC URLs (override config)")
        workers := flag.Int("workers", 0, "jumlah worker (0=auto)")
        onlyFunded := flag.Bool("funded", false, "hanya tampilkan yang punya balance")
        outputFile := flag.String("o", "", "simpan funded address ke file")
        minBalance := flag.Float64("min", 0, "minimum ETH balance")
        flag.Parse()

        printBanner()

        if *fileFlag == "" && *addrFlag == "" {
                boldRed.Println("\n[ERROR] Berikan -f <file> atau -addr <address>")
                flag.Usage()
                os.Exit(1)
        }

        cfg, err := config.Load(*cfgFile)
        if err != nil {
                boldRed.Printf("[WARN] Config: %v — pakai default\n", err)
                cfg = config.Default()
        }

        // ── Build endpoint specs (gabung URL biasa + auth endpoints) ──
        var specs []rpc.EndpointSpec
        if *rpcURLs != "" {
                for _, u := range strings.Split(*rpcURLs, ",") {
                        u = strings.TrimSpace(u)
                        if u != "" {
                                specs = append(specs, rpc.EndpointSpec{URL: u})
                        }
                }
        } else {
                for _, u := range cfg.RPC.Endpoints {
                        specs = append(specs, rpc.EndpointSpec{URL: u})
                }
                for _, ae := range cfg.RPC.EndpointsAuth {
                        specs = append(specs, rpc.EndpointSpec{URL: ae.URL, Headers: ae.Headers})
                }
        }
        if len(specs) == 0 {
                boldRed.Println("[ERROR] Tidak ada RPC endpoint dikonfigurasi")
                os.Exit(1)
        }

        // ── Worker count ──
        nWorkers := *workers
        if nWorkers <= 0 {
                nWorkers = cfg.Workers.Checker
        }
        if nWorkers <= 0 {
                nWorkers = runtime.NumCPU() * 3
        }

        // ── Token list ──
        var tokenChecks []rpc.TokenCheck
        if cfg.Tokens.CheckERC20 {
                for _, t := range cfg.Tokens.List {
                        tokenChecks = append(tokenChecks, rpc.TokenCheck{
                                Name: t.Name, Address: t.Address, Decimals: t.Decimals,
                        })
                }
        }

        // ── Addresses ──
        var addresses []string
        if *addrFlag != "" {
                if !wallet.IsValidAddress(*addrFlag) {
                        boldRed.Printf("[ERROR] Invalid address: %s\n", *addrFlag)
                        os.Exit(1)
                }
                addresses = []string{common.HexToAddress(*addrFlag).Hex()}
        } else {
                addresses, err = loadAddresses(*fileFlag)
                if err != nil {
                        boldRed.Printf("[ERROR] Load file: %v\n", err)
                        os.Exit(1)
                }
        }

        if len(addresses) == 0 {
                boldRed.Println("[ERROR] Tidak ada address valid ditemukan")
                os.Exit(1)
        }

        // ── RPC Manager ──
        httpClient := &http.Client{
                Timeout: cfg.RPC.Timeout,
                Transport: &http.Transport{
                        MaxIdleConns:        200,
                        MaxIdleConnsPerHost: 50,
                        IdleConnTimeout:     60 * time.Second,
                },
        }
        rpcMgr := rpc.NewManager(specs, httpClient,
                cfg.RPC.Timeout, cfg.RPC.Retries,
                cfg.RPC.DeadThreshold, cfg.RPC.DeadCooldown,
                cfg.RPC.RateLimit)

        // ── RPC Health Check ──
        boldYellow.Printf("\n[RPC] Mengecek %d endpoint...\n", len(specs))
        hcResults := rpcMgr.HealthCheck(5 * time.Second)
        aliveCount := 0
        for _, s := range hcResults {
                if s.Alive {
                        aliveCount++
                        boldGreen.Printf("  ✓ %-50s  %s\n", s.URL, s.Latency.Round(time.Millisecond))
                } else {
                        boldRed.Printf("  ✗ %-50s  TIMEOUT/ERROR\n", s.URL)
                }
        }
        if aliveCount == 0 {
                boldRed.Println("\n[ERROR] Semua RPC endpoint tidak merespons. Periksa koneksi atau tambah endpoint baru di config.yaml")
                os.Exit(1)
        }
        boldYellow.Printf("[RPC] %d/%d endpoint aktif\n", aliveCount, len(specs))

        // ── Config display ──
        boldYellow.Printf("\n[CONFIG] Network:    Ethereum Mainnet (ETH)\n")
        boldYellow.Printf("[CONFIG] Addresses:  %d | Workers: %d | Batch: %d\n", len(addresses), nWorkers, cfg.RPC.BatchSize)
        if len(tokenChecks) > 0 {
                names := make([]string, len(tokenChecks))
                for i, t := range tokenChecks {
                        names[i] = t.Name
                }
                boldYellow.Printf("[CONFIG] Tokens:     %s\n", strings.Join(names, ", "))
        }
        fmt.Println()

        ctx := context.Background()

        // ── Batch processing ──
        batches := makeBatches(addresses, cfg.RPC.BatchSize)
        jobs := make(chan []string, nWorkers*2)
        results := make(chan CheckResult, nWorkers*4)

        tokenDecimalsMap := make(map[string]int, len(tokenChecks))
        for _, tc := range tokenChecks {
                tokenDecimalsMap[tc.Name] = tc.Decimals
        }

        var wg sync.WaitGroup
        for i := 0; i < nWorkers; i++ {
                wg.Add(1)
                go func() {
                        defer wg.Done()
                        for batch := range jobs {
                                batchResults, err := rpcMgr.GetBalanceBatch(ctx, batch, tokenChecks)
                                for _, addr := range batch {
                                        cr := CheckResult{Address: addr, Tokens: map[string]*big.Float{}}
                                        if err != nil {
                                                cr.Error = err
                                        } else if ar, ok := batchResults[addr]; ok {
                                                cr.ETHBal = rpc.WeiToDecimal(ar.ETH, 18)
                                                cr.HasBal = rpc.HasAnyBalance(ar)
                                                for tName, tWei := range ar.Tokens {
                                                        dec := tokenDecimalsMap[tName]
                                                        if dec == 0 {
                                                                dec = 18
                                                        }
                                                        cr.Tokens[tName] = rpc.WeiToDecimal(tWei, dec)
                                                }
                                        }
                                        results <- cr
                                }
                        }
                }()
        }

        go func() {
                for _, batch := range batches {
                        jobs <- batch
                }
                close(jobs)
                wg.Wait()
                close(results)
        }()

        // ── Progress bar ──
        total := int64(len(addresses))
        var checked atomic.Int64
        progressDone := make(chan struct{})
        go func() {
                defer close(progressDone)
                ticker := time.NewTicker(200 * time.Millisecond)
                defer ticker.Stop()
                startT := time.Now()
                bw := 25
                for {
                        select {
                        case <-ticker.C:
                                cur := checked.Load()
                                pct := 0.0
                                if total > 0 {
                                        pct = float64(cur) / float64(total)
                                }
                                filled := int(pct * float64(bw))
                                if filled > bw {
                                        filled = bw
                                }
                                bar := strings.Repeat("█", filled) + strings.Repeat("░", bw-filled)
                                elapsed := time.Since(startT).Seconds()
                                speed := 0.0
                                if elapsed > 0 {
                                        speed = float64(cur) / elapsed
                                }
                                fmt.Fprintf(os.Stderr, "\r[%s] %d/%d (%.1f%%) | %.0f addr/s | RPC aktif: %d   ",
                                        bar, cur, total, pct*100, speed, rpcMgr.AliveCount())
                                if cur >= total {
                                        fmt.Fprintln(os.Stderr)
                                        return
                                }
                        }
                }
        }()

        // ── Collect results ──
        startTime := time.Now()
        var totalFunded, totalErrors atomic.Int64
        var fundedWallets []CheckResult
        var mu sync.Mutex
        minBal := new(big.Float).SetFloat64(*minBalance)

        printCh := make(chan CheckResult, 200)
        var printWg sync.WaitGroup
        printWg.Add(1)
        go func() {
                defer printWg.Done()
                seq := int64(0)
                for r := range printCh {
                        seq++
                        if r.Error != nil {
                                boldRed.Printf("[%04d] %-44s  ERROR: %v\n", seq, r.Address, r.Error)
                                continue
                        }
                        ethF, _ := r.ETHBal.Float64()
                        if r.HasBal && r.ETHBal.Cmp(minBal) >= 0 {
                                boldGreen.Printf("[%04d] ", seq)
                                boldWhite.Printf("%-44s  ", r.Address)
                                boldGreen.Printf("💰 %.8f ETH", ethF)
                                for tName, tBal := range r.Tokens {
                                        tF, _ := tBal.Float64()
                                        if tF > 0 {
                                                boldGreen.Printf(" | %s: %.4f", tName, tF)
                                        }
                                }
                                fmt.Println()
                        } else if !*onlyFunded {
                                dimWhite.Printf("[%04d] %-44s  %.8f ETH\n", seq, r.Address, ethF)
                        }
                }
        }()

        for r := range results {
                checked.Add(1)
                if r.Error != nil {
                        totalErrors.Add(1)
                } else if r.HasBal && r.ETHBal.Cmp(minBal) >= 0 {
                        totalFunded.Add(1)
                        mu.Lock()
                        fundedWallets = append(fundedWallets, r)
                        mu.Unlock()
                }
                printCh <- r
        }
        close(printCh)
        printWg.Wait()
        <-progressDone

        elapsed := time.Since(startTime)
        speed := float64(checked.Load()) / elapsed.Seconds()

        fmt.Println()
        boldCyan.Println("════════════════════════════════════════════════════")
        boldWhite.Printf("  Total Checked:  %d\n", checked.Load())
        boldGreen.Printf("  Funded:         %d\n", totalFunded.Load())
        boldRed.Printf("  Errors:         %d\n", totalErrors.Load())
        boldYellow.Printf("  Speed:          %.1f addr/sec\n", speed)
        boldCyan.Printf("  Time:           %s\n", elapsed.Round(time.Millisecond))
        boldCyan.Println("════════════════════════════════════════════════════")

        if len(fundedWallets) > 0 {
                fmt.Println()
                boldGreen.Printf("🎯 FUNDED ADDRESSES:\n")
                for _, fw := range fundedWallets {
                        ethF, _ := fw.ETHBal.Float64()
                        boldGreen.Printf("  %s  %.8f ETH\n", fw.Address, ethF)
                }
        }

        if *outputFile != "" && len(fundedWallets) > 0 {
                f, err := os.Create(*outputFile)
                if err != nil {
                        boldRed.Printf("\n[ERROR] Create output: %v\n", err)
                } else {
                        w := bufio.NewWriter(f)
                        fmt.Fprintln(w, "# Funded Addresses")
                        fmt.Fprintf(w, "# Generated: %s\n\n", time.Now().Format(time.RFC3339))
                        for _, fw := range fundedWallets {
                                ethF, _ := fw.ETHBal.Float64()
                                fmt.Fprintf(w, "%s,%.18f\n", fw.Address, ethF)
                        }
                        w.Flush()
                        f.Close()
                        boldGreen.Printf("\n💾 Saved to: %s\n", *outputFile)
                }
        }

        fmt.Println()
}
