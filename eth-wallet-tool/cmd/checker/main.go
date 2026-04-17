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

        "eth-wallet-tool/internal/wallet"
)

var (
        boldCyan    = color.New(color.FgCyan, color.Bold)
        boldGreen   = color.New(color.FgGreen, color.Bold)
        boldYellow  = color.New(color.FgYellow, color.Bold)
        boldMagenta = color.New(color.FgMagenta, color.Bold)
        boldRed     = color.New(color.FgRed, color.Bold)
        boldWhite   = color.New(color.FgWhite, color.Bold)
        dimWhite    = color.New(color.FgWhite, color.Faint)
)

// RPCRequest is a JSON-RPC 2.0 request
type RPCRequest struct {
        JSONRPC string        `json:"jsonrpc"`
        Method  string        `json:"method"`
        Params  []interface{} `json:"params"`
        ID      int           `json:"id"`
}

// RPCResponse is a JSON-RPC 2.0 response
type RPCResponse struct {
        JSONRPC string      `json:"jsonrpc"`
        ID      int         `json:"id"`
        Result  interface{} `json:"result"`
        Error   *RPCError   `json:"error"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
        Code    int    `json:"code"`
        Message string `json:"message"`
}

// CheckResult holds the result of a balance check
type CheckResult struct {
        Address    string
        Balance    *big.Float
        BalanceWei *big.Int
        HasBalance bool
        Error      error
        Duration   time.Duration
        RPC        string
}

// RPCPool manages a pool of RPC endpoints for load balancing
type RPCPool struct {
        endpoints  []string
        mu         sync.Mutex
        idx        int
        httpClient *http.Client
        retries    int
        delay      time.Duration
}

// NewRPCPool creates a new RPC endpoint pool
func NewRPCPool(endpoints []string, timeout time.Duration, retries int, delay time.Duration) *RPCPool {
        return &RPCPool{
                endpoints: endpoints,
                httpClient: &http.Client{
                        Timeout: timeout,
                        Transport: &http.Transport{
                                MaxIdleConns:        500,
                                MaxIdleConnsPerHost: 100,
                                IdleConnTimeout:     90 * time.Second,
                                DisableCompression:  false,
                        },
                },
                retries: retries,
                delay:   delay,
        }
}

// nextEndpoint returns the next RPC endpoint (round-robin)
func (p *RPCPool) nextEndpoint() string {
        p.mu.Lock()
        defer p.mu.Unlock()
        ep := p.endpoints[p.idx%len(p.endpoints)]
        p.idx++
        return ep
}

// GetBalance fetches ETH balance for an address with retry + round-robin
func (p *RPCPool) GetBalance(ctx context.Context, address string) (balanceWei *big.Int, usedRPC string, err error) {
        req := RPCRequest{
                JSONRPC: "2.0",
                Method:  "eth_getBalance",
                Params:  []interface{}{address, "latest"},
                ID:      1,
        }

        for attempt := 0; attempt <= p.retries; attempt++ {
                if attempt > 0 {
                        select {
                        case <-ctx.Done():
                                return nil, "", ctx.Err()
                        case <-time.After(time.Duration(attempt) * p.delay):
                        }
                }

                endpoint := p.nextEndpoint()
                usedRPC = endpoint

                body, marshalErr := json.Marshal(req)
                if marshalErr != nil {
                        return nil, endpoint, fmt.Errorf("marshal error: %w", marshalErr)
                }

                httpReq, reqErr := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
                if reqErr != nil {
                        err = fmt.Errorf("request error: %w", reqErr)
                        continue
                }
                httpReq.Header.Set("Content-Type", "application/json")

                resp, doErr := p.httpClient.Do(httpReq)
                if doErr != nil {
                        err = fmt.Errorf("http error: %w", doErr)
                        continue
                }

                respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
                resp.Body.Close()
                if readErr != nil {
                        err = fmt.Errorf("read error: %w", readErr)
                        continue
                }

                var rpcResp RPCResponse
                if unmarshalErr := json.Unmarshal(respBody, &rpcResp); unmarshalErr != nil {
                        err = fmt.Errorf("unmarshal error: %w (body: %s)", unmarshalErr, string(respBody[:min(100, len(respBody))]))
                        continue
                }

                if rpcResp.Error != nil {
                        // 429 rate limit — retry with next endpoint
                        if rpcResp.Error.Code == 429 {
                                err = fmt.Errorf("rate-limited on %s", shortURL(endpoint))
                                continue
                        }
                        return nil, endpoint, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
                }

                hexBalance, ok := rpcResp.Result.(string)
                if !ok {
                        err = fmt.Errorf("unexpected result type: %T", rpcResp.Result)
                        continue
                }

                hexBalance = strings.TrimPrefix(hexBalance, "0x")
                if hexBalance == "" {
                        hexBalance = "0"
                }

                balWei := new(big.Int)
                balWei.SetString(hexBalance, 16)
                return balWei, endpoint, nil
        }

        return nil, usedRPC, err
}

func shortURL(u string) string {
        u = strings.TrimPrefix(u, "https://")
        u = strings.TrimPrefix(u, "http://")
        if len(u) > 30 {
                return u[:30] + "..."
        }
        return u
}

func min(a, b int) int {
        if a < b {
                return a
        }
        return b
}

// weiToEther converts Wei to Ether
func weiToEther(wei *big.Int) *big.Float {
        divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
        ethValue := new(big.Float).SetInt(wei)
        ethValue.Quo(ethValue, divisor)
        return ethValue
}

func printBanner() {
        boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET CHECKER  v1.0                  ║
║    High-Speed Ethereum Balance Checker            ║
╚═══════════════════════════════════════════════════╝`)
}

// Default public RPC endpoints (free, no API key needed)
var defaultRPCs = []string{
        "https://eth.llamarpc.com",
        "https://cloudflare-eth.com",
        "https://ethereum.publicnode.com",
        "https://rpc.payload.de",
        "https://eth-mainnet.public.blastapi.io",
}

func loadAddresses(filePath string) ([]string, error) {
        f, err := os.Open(filePath)
        if err != nil {
                return nil, fmt.Errorf("cannot open file: %w", err)
        }
        defer f.Close()

        var addresses []string
        scanner := bufio.NewScanner(f)
        for scanner.Scan() {
                line := strings.TrimSpace(scanner.Text())
                if line == "" || strings.HasPrefix(line, "#") {
                        continue
                }
                // Support both raw address and "address:privkey" or "index,address,privkey" CSV
                parts := strings.FieldsFunc(line, func(r rune) bool { return r == ':' || r == ',' })
                var addr string
                for _, p := range parts {
                        p = strings.TrimSpace(p)
                        if wallet.IsValidAddress(p) {
                                addr = common.HexToAddress(p).Hex()
                                break
                        }
                }
                if addr != "" {
                        addresses = append(addresses, addr)
                }
        }
        return addresses, scanner.Err()
}

func main() {
        fileFlag := flag.String("f", "", "file with addresses to check (one per line, CSV, or address:privkey)")
        addrFlag := flag.String("addr", "", "single address to check")
        rpcURLs := flag.String("rpc", "", "comma-separated RPC endpoint URLs (default: 5 public RPCs)")
        workers := flag.Int("workers", runtime.NumCPU()*2, "number of concurrent workers")
        timeout := flag.Duration("timeout", 15*time.Second, "RPC request timeout per request")
        retries := flag.Int("retries", len(defaultRPCs)-1, "number of retries (switches RPC on each retry)")
        delay := flag.Duration("delay", 200*time.Millisecond, "delay between retries")
        onlyFunded := flag.Bool("funded", false, "only show addresses with balance > 0")
        outputFile := flag.String("o", "", "save funded addresses to file")
        minBalance := flag.Float64("min", 0, "minimum ETH balance threshold to highlight")
        flag.Parse()

        printBanner()

        if *fileFlag == "" && *addrFlag == "" {
                boldRed.Println("\n[ERROR] Provide -f <addresses_file> or -addr <address>")
                fmt.Println()
                flag.Usage()
                os.Exit(1)
        }

        // Parse RPC endpoints
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
                        boldRed.Printf("[ERROR] Loading addresses: %v\n", err)
                        os.Exit(1)
                }
        }

        if len(addresses) == 0 {
                boldRed.Println("[ERROR] No valid addresses found")
                os.Exit(1)
        }

        boldYellow.Printf("\n[CONFIG] Checking %d addresses\n", len(addresses))
        boldYellow.Printf("[CONFIG] Workers: %d | Retries: %d | Delay: %s\n", *workers, *retries, *delay)
        boldYellow.Printf("[CONFIG] RPC Endpoints (%d):\n", len(endpoints))
        for i, ep := range endpoints {
                dimWhite.Printf("         [%d] %s\n", i+1, ep)
        }
        fmt.Println()

        pool := NewRPCPool(endpoints, *timeout, *retries, *delay)
        ctx := context.Background()

        type job struct {
                idx     int
                address string
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
                                balWei, rpcUsed, err := pool.GetBalance(ctx, j.address)
                                dur := time.Since(start)
                                res := CheckResult{
                                        Address:  j.address,
                                        Duration: dur,
                                        Error:    err,
                                        RPC:      shortURL(rpcUsed),
                                }
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
                        jobs <- job{idx: i + 1, address: addr}
                }
                close(jobs)
                wg.Wait()
                close(results)
        }()

        start := time.Now()
        var totalChecked atomic.Int64
        var totalFunded atomic.Int64
        var totalErrors atomic.Int64
        var fundedWallets []CheckResult
        var mu sync.Mutex

        minBal := new(big.Float).SetFloat64(*minBalance)

        // Print results as they arrive
        type printJob struct {
                r   CheckResult
                seq int64
        }
        printCh := make(chan printJob, 100)

        var printWg sync.WaitGroup
        printWg.Add(1)
        go func() {
                defer printWg.Done()
                for pj := range printCh {
                        r := pj.r
                        seq := pj.seq
                        if r.Error != nil {
                                boldRed.Printf("[%04d] %-44s  ", seq, r.Address)
                                boldRed.Printf("ERROR: %v\n", r.Error)
                                continue
                        }

                        balStr := fmt.Sprintf("%.8f ETH", r.Balance)
                        cmp := r.Balance.Cmp(minBal)

                        if r.HasBalance && cmp >= 0 {
                                boldGreen.Printf("[%04d] ", seq)
                                boldWhite.Printf("%-44s  ", r.Address)
                                boldGreen.Printf("💰 %-22s", balStr)
                                dimWhite.Printf("  [%s]  (%s)\n", r.RPC, r.Duration.Round(time.Millisecond))
                        } else if !*onlyFunded {
                                dimWhite.Printf("[%04d] %-44s  %-22s  [%s]  (%s)\n",
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
                printCh <- printJob{r: r, seq: seq}
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
                        boldRed.Printf("\n[ERROR] Creating output file: %v\n", err)
                } else {
                        defer f.Close()
                        w := bufio.NewWriter(f)
                        fmt.Fprintln(w, "# Funded ETH Addresses")
                        fmt.Fprintf(w, "# Generated at: %s\n\n", time.Now().Format(time.RFC3339))
                        for _, fw := range fundedWallets {
                                balF, _ := fw.Balance.Float64()
                                fmt.Fprintf(w, "%s,%.18f\n", fw.Address, balF)
                        }
                        w.Flush()
                        boldGreen.Printf("\n💾 Saved to: %s\n", *outputFile)
                }
        }

        _ = boldMagenta
        fmt.Println()
}
