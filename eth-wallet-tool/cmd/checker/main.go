package main

import (
        "bufio"
        "context"
        "encoding/json"
        "flag"
        "fmt"
        "io"
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

        "eth-wallet-tool/internal/proxy"
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

var defaultRPCs = []string{
        "https://eth.llamarpc.com",
        "https://ethereum.publicnode.com",
        "https://eth-mainnet.public.blastapi.io",
        "https://rpc.payload.de",
        "https://1rpc.io/eth",
}

type RPCRequest struct {
        JSONRPC string        `json:"jsonrpc"`
        Method  string        `json:"method"`
        Params  []interface{} `json:"params"`
        ID      int           `json:"id"`
}

type RPCResponse struct {
        Result interface{} `json:"result"`
        Error  *struct {
                Code    int    `json:"code"`
                Message string `json:"message"`
        } `json:"error"`
}

type CheckResult struct {
        Address    string
        Balance    *big.Float
        BalanceWei *big.Int
        HasBalance bool
        Error      error
        Duration   time.Duration
        RPC        string
}

type Checker struct {
        endpoints  []string
        pm         *proxy.Manager
        useProxy   bool
        httpClient *http.Client
        mu         sync.Mutex
        idx        int
        retries    int
        delay      time.Duration
        timeout    time.Duration
}

func NewChecker(endpoints []string, pm *proxy.Manager, useProxy bool, timeout time.Duration, retries int, delay time.Duration) *Checker {
        return &Checker{
                endpoints: endpoints,
                pm:        pm,
                useProxy:  useProxy,
                httpClient: proxy.BuildHTTPClient("", timeout),
                retries:   retries,
                delay:     delay,
                timeout:   timeout,
        }
}

func (c *Checker) nextEndpoint() string {
        c.mu.Lock()
        defer c.mu.Unlock()
        ep := c.endpoints[c.idx%len(c.endpoints)]
        c.idx++
        return ep
}

func (c *Checker) GetBalance(ctx context.Context, address string) (balWei *big.Int, usedRPC string, err error) {
        req := RPCRequest{
                JSONRPC: "2.0", Method: "eth_getBalance",
                Params: []interface{}{address, "latest"}, ID: 1,
        }

        for attempt := 0; attempt <= c.retries; attempt++ {
                if attempt > 0 {
                        select {
                        case <-ctx.Done():
                                return nil, "", ctx.Err()
                        case <-time.After(time.Duration(attempt) * c.delay):
                        }
                }

                endpoint := c.nextEndpoint()
                usedRPC = endpoint

                var client *http.Client
                if c.useProxy && c.pm != nil && c.pm.Count() > 0 {
                        px := c.pm.Next()
                        if px != nil {
                                client = proxy.BuildHTTPClient(px.Address, c.timeout)
                        } else {
                                client = c.httpClient
                        }
                } else {
                        client = c.httpClient
                }

                body, _ := json.Marshal(req)
                httpReq, reqErr := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
                if reqErr != nil {
                        err = reqErr
                        continue
                }
                httpReq.Header.Set("Content-Type", "application/json")

                resp, doErr := client.Do(httpReq)
                if doErr != nil {
                        err = fmt.Errorf("http: %w", doErr)
                        continue
                }

                respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<15))
                resp.Body.Close()

                var rpcResp RPCResponse
                if unmarshalErr := json.Unmarshal(respBody, &rpcResp); unmarshalErr != nil {
                        err = fmt.Errorf("parse error")
                        continue
                }

                if rpcResp.Error != nil {
                        if rpcResp.Error.Code == 429 {
                                err = fmt.Errorf("rate-limited")
                                continue
                        }
                        return nil, endpoint, fmt.Errorf("rpc %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
                }

                hexBal, ok := rpcResp.Result.(string)
                if !ok {
                        err = fmt.Errorf("bad result")
                        continue
                }
                hexBal = strings.TrimPrefix(hexBal, "0x")
                if hexBal == "" {
                        hexBal = "0"
                }
                n := new(big.Int)
                n.SetString(hexBal, 16)
                return n, endpoint, nil
        }
        return nil, usedRPC, err
}

func weiToEther(wei *big.Int) *big.Float {
        d := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
        return new(big.Float).Quo(new(big.Float).SetInt(wei), d)
}

func shortURL(u string) string {
        u = strings.TrimPrefix(u, "https://")
        u = strings.TrimPrefix(u, "http://")
        if len(u) > 35 {
                return u[:35] + "..."
        }
        return u
}

func printBanner() {
        boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET CHECKER  v2.0                  ║
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

func main() {
        fileFlag := flag.String("f", "", "file berisi daftar address")
        addrFlag := flag.String("addr", "", "single address untuk dicek")
        rpcURLs := flag.String("rpc", "", "comma-separated RPC URLs")
        workers := flag.Int("workers", runtime.NumCPU()*2, "jumlah worker concurrent")
        timeout := flag.Duration("timeout", 15*time.Second, "timeout per request")
        retries := flag.Int("retries", 4, "jumlah retry per address")
        delay := flag.Duration("delay", 200*time.Millisecond, "delay antar retry")
        onlyFunded := flag.Bool("funded", false, "hanya tampilkan yang punya balance")
        outputFile := flag.String("o", "", "simpan funded address ke file")
        minBalance := flag.Float64("min", 0, "minimum ETH balance")
        useProxyFlag := flag.Bool("proxy", false, "gunakan proxy dari proxies.txt")
        proxyFile := flag.String("pfile", "proxies.txt", "path file proxy")
        fetchProxy := flag.Bool("fetch-proxy", false, "fetch & validasi proxy baru dari internet")
        validateWorkers := flag.Int("proxy-workers", 50, "jumlah worker validasi proxy")
        flag.Parse()

        printBanner()

        if *fileFlag == "" && *addrFlag == "" {
                boldRed.Println("\n[ERROR] Berikan -f <file> atau -addr <address>")
                flag.Usage()
                os.Exit(1)
        }

        // ── RPC Endpoints ──
        var endpoints []string
        if *rpcURLs != "" {
                for _, u := range strings.Split(*rpcURLs, ",") {
                        u = strings.TrimSpace(u)
                        if u != "" {
                                endpoints = append(endpoints, u)
                        }
                }
        } else {
                endpoints = defaultRPCs
        }

        // ── Proxy Manager ──
        pm := proxy.NewManager(*proxyFile, 3)
        pm.OnRefetch = func(count int) {
                boldGreen.Printf("\n[PROXY] +%d proxy baru berhasil divalidasi\n", count)
        }
        pm.OnRemove = func(addr string) {
                dimWhite.Printf("[PROXY] Buang proxy mati: %s\n", addr)
        }

        ctx := context.Background()

        if *fetchProxy || *useProxyFlag {
                pm.Load()
                if *fetchProxy || pm.Count() == 0 {
                        boldYellow.Printf("[PROXY] Mengambil proxy dari %d sumber...\n", len(proxy.ProxySources))
                        pm.FetchAndValidate(ctx, *validateWorkers)
                        boldGreen.Printf("[PROXY] %d proxy valid ditemukan\n", pm.Count())
                } else {
                        boldGreen.Printf("[PROXY] Loaded %d proxy dari %s\n", pm.Count(), *proxyFile)
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
                var err error
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

        boldYellow.Printf("\n[CONFIG] Checking %d addresses\n", len(addresses))
        boldYellow.Printf("[CONFIG] Workers: %d | Retries: %d | Delay: %s\n", *workers, *retries, *delay)
        boldYellow.Printf("[CONFIG] RPC Endpoints (%d):\n", len(endpoints))
        for i, ep := range endpoints {
                dimWhite.Printf("         [%d] %s\n", i+1, ep)
        }
        if *useProxyFlag {
                boldYellow.Printf("[CONFIG] Proxy: ON (%d aktif)\n", pm.Count())
        }
        fmt.Println()

        checker := NewChecker(endpoints, pm, *useProxyFlag, *timeout, *retries, *delay)

        type job struct {
                idx  int
                addr string
        }
        jobs := make(chan job, *workers*2)
        results := make(chan CheckResult, *workers*2)

        var wg sync.WaitGroup
        for i := 0; i < *workers; i++ {
                wg.Add(1)
                go func() {
                        defer wg.Done()
                        for j := range jobs {
                                start := time.Now()
                                balWei, rpcUsed, err := checker.GetBalance(ctx, j.addr)
                                dur := time.Since(start)
                                res := CheckResult{Address: j.addr, Duration: dur, Error: err, RPC: shortURL(rpcUsed)}
                                if err == nil {
                                        res.BalanceWei = balWei
                                        res.Balance = weiToEther(balWei)
                                        res.HasBalance = balWei.Cmp(big.NewInt(0)) > 0
                                }
                                results <- res
                        }
                }()
        }

        go func() {
                for i, addr := range addresses {
                        jobs <- job{idx: i + 1, addr: addr}
                }
                close(jobs)
                wg.Wait()
                close(results)
        }()

        start := time.Now()
        var totalChecked, totalFunded, totalErrors atomic.Int64
        var fundedWallets []CheckResult
        var mu sync.Mutex
        minBal := new(big.Float).SetFloat64(*minBalance)

        type pJob struct {
                r   CheckResult
                seq int64
        }
        printCh := make(chan pJob, 100)
        var printWg sync.WaitGroup
        printWg.Add(1)
        go func() {
                defer printWg.Done()
                for pj := range printCh {
                        r, seq := pj.r, pj.seq
                        if r.Error != nil {
                                boldRed.Printf("[%04d] %-44s  ERROR: %v\n", seq, r.Address, r.Error)
                                continue
                        }
                        balStr := fmt.Sprintf("%.8f ETH", r.Balance)
                        if r.HasBalance && r.Balance.Cmp(minBal) >= 0 {
                                boldGreen.Printf("[%04d] ", seq)
                                boldWhite.Printf("%-44s  ", r.Address)
                                boldGreen.Printf("💰 %-24s", balStr)
                                dimWhite.Printf("[%s]  (%s)\n", r.RPC, r.Duration.Round(time.Millisecond))
                        } else if !*onlyFunded {
                                dimWhite.Printf("[%04d] %-44s  %-24s [%s]  (%s)\n",
                                        seq, r.Address, balStr, r.RPC, r.Duration.Round(time.Millisecond))
                        }
                }
        }()

        for r := range results {
                totalChecked.Add(1)
                seq := totalChecked.Load()
                if r.Error != nil {
                        totalErrors.Add(1)
                } else if r.HasBalance && r.Balance.Cmp(minBal) >= 0 {
                        totalFunded.Add(1)
                        mu.Lock()
                        fundedWallets = append(fundedWallets, r)
                        mu.Unlock()
                }
                printCh <- pJob{r: r, seq: seq}
        }
        close(printCh)
        printWg.Wait()

        elapsed := time.Since(start)
        speed := float64(totalChecked.Load()) / elapsed.Seconds()

        fmt.Println()
        boldCyan.Println("════════════════════════════════════════════════════")
        boldWhite.Printf("  Total Checked:  %d\n", totalChecked.Load())
        boldGreen.Printf("  Funded:         %d\n", totalFunded.Load())
        boldRed.Printf("  Errors:         %d\n", totalErrors.Load())
        boldYellow.Printf("  Speed:          %.1f addr/sec\n", speed)
        boldCyan.Printf("  Time:           %s\n", elapsed.Round(time.Millisecond))
        boldCyan.Println("════════════════════════════════════════════════════")

        if len(fundedWallets) > 0 {
                fmt.Println()
                boldGreen.Printf("🎯 FUNDED ADDRESSES:\n")
                for _, fw := range fundedWallets {
                        boldGreen.Printf("  %s  %.8f ETH\n", fw.Address, fw.Balance)
                }
        }

        if *outputFile != "" && len(fundedWallets) > 0 {
                f, err := os.Create(*outputFile)
                if err != nil {
                        boldRed.Printf("\n[ERROR] Create output: %v\n", err)
                } else {
                        w := bufio.NewWriter(f)
                        fmt.Fprintln(w, "# Funded ETH Addresses")
                        fmt.Fprintf(w, "# Generated: %s\n\n", time.Now().Format(time.RFC3339))
                        for _, fw := range fundedWallets {
                                balF, _ := fw.Balance.Float64()
                                fmt.Fprintf(w, "%s,%.18f\n", fw.Address, balF)
                        }
                        w.Flush()
                        f.Close()
                        boldGreen.Printf("\n💾 Saved to: %s\n", *outputFile)
                }
        }

        // Simpan proxy aktif
        if *useProxyFlag && pm.Count() > 0 {
                pm.Save()
        }
        fmt.Println()
}
