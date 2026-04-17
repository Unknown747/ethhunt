// eth-hunt: Generator + Checker otomatis terintegrasi dengan proxy support
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
        "os/signal"
        "path/filepath"
        "runtime"
        "strings"
        "sync"
        "sync/atomic"
        "syscall"
        "time"

        "github.com/fatih/color"

        "eth-wallet-tool/internal/proxy"
        "eth-wallet-tool/internal/wallet"
)

// ─── Warna output ────────────────────────────────────────────────────────────

var (
        cCyan    = color.New(color.FgCyan, color.Bold)
        cGreen   = color.New(color.FgGreen, color.Bold)
        cYellow  = color.New(color.FgYellow, color.Bold)
        cRed     = color.New(color.FgRed, color.Bold)
        cWhite   = color.New(color.FgWhite, color.Bold)
        cDim     = color.New(color.FgWhite, color.Faint)
        cMagenta = color.New(color.FgMagenta, color.Bold)
)

// ─── Default RPC endpoints ───────────────────────────────────────────────────

var defaultRPCs = []string{
        "https://eth.llamarpc.com",
        "https://ethereum.publicnode.com",
        "https://eth-mainnet.public.blastapi.io",
        "https://rpc.payload.de",
        "https://1rpc.io/eth",
}

// ─── RPC helper ──────────────────────────────────────────────────────────────

type rpcReq struct {
        JSONRPC string        `json:"jsonrpc"`
        Method  string        `json:"method"`
        Params  []interface{} `json:"params"`
        ID      int           `json:"id"`
}

type rpcResp struct {
        Result interface{} `json:"result"`
        Error  *struct {
                Code    int    `json:"code"`
                Message string `json:"message"`
        } `json:"error"`
}

// getBalance mengecek ETH balance melalui satu HTTP client
func getBalance(ctx context.Context, client *http.Client, rpcURL, address string) (*big.Int, error) {
        body, _ := json.Marshal(rpcReq{
                JSONRPC: "2.0", Method: "eth_getBalance",
                Params: []interface{}{address, "latest"}, ID: 1,
        })

        req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, strings.NewReader(string(body)))
        if err != nil {
                return nil, err
        }
        req.Header.Set("Content-Type", "application/json")

        resp, err := client.Do(req)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()

        data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<15))
        if err != nil {
                return nil, err
        }

        var r rpcResp
        if err := json.Unmarshal(data, &r); err != nil {
                return nil, fmt.Errorf("unmarshal: %w", err)
        }
        if r.Error != nil {
                if r.Error.Code == 429 {
                        return nil, fmt.Errorf("rate-limited")
                }
                return nil, fmt.Errorf("rpc %d: %s", r.Error.Code, r.Error.Message)
        }

        hexStr, ok := r.Result.(string)
        if !ok {
                return nil, fmt.Errorf("bad result type")
        }
        hexStr = strings.TrimPrefix(hexStr, "0x")
        if hexStr == "" {
                hexStr = "0"
        }
        n := new(big.Int)
        n.SetString(hexStr, 16)
        return n, nil
}

// ─── Stats ────────────────────────────────────────────────────────────────────

type Stats struct {
        Generated atomic.Int64
        Checked   atomic.Int64
        Found     atomic.Int64
        Errors    atomic.Int64
        startTime time.Time
}

func (s *Stats) genRate() float64 {
        elapsed := time.Since(s.startTime).Seconds()
        if elapsed == 0 {
                return 0
        }
        return float64(s.Generated.Load()) / elapsed
}

func (s *Stats) checkRate() float64 {
        elapsed := time.Since(s.startTime).Seconds()
        if elapsed == 0 {
                return 0
        }
        return float64(s.Checked.Load()) / elapsed
}

// ─── Result ───────────────────────────────────────────────────────────────────

type HuntResult struct {
        w       *wallet.Wallet
        balance *big.Float
}

// ─── Banner ───────────────────────────────────────────────────────────────────

func printBanner() {
        cCyan.Print(`
╔════════════════════════════════════════════════════════╗
║           ETH WALLET HUNTER  v2.0                      ║
║   Auto Generate + Check + Proxy Support                ║
╚════════════════════════════════════════════════════════╝
`)
}

func weiToEth(wei *big.Int) *big.Float {
        divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
        return new(big.Float).Quo(new(big.Float).SetInt(wei), divisor)
}

// ─── Worker Pool ─────────────────────────────────────────────────────────────

type workerPool struct {
        jobs     chan *wallet.Wallet
        results  chan HuntResult
        rpcList  []string
        pm       *proxy.Manager
        useProxy bool
        timeout  time.Duration
        retries  int
        ctx      context.Context // dipakai untuk fast shutdown
        wg       sync.WaitGroup
        stats    *Stats
        rpcIdx   atomic.Int64
}

func newWorkerPool(ctx context.Context, n int, rpcList []string, pm *proxy.Manager, useProxy bool, timeout time.Duration, retries int, stats *Stats) *workerPool {
        wp := &workerPool{
                jobs:     make(chan *wallet.Wallet, n*4),
                results:  make(chan HuntResult, n*2),
                rpcList:  rpcList,
                pm:       pm,
                useProxy: useProxy,
                timeout:  timeout,
                retries:  retries,
                ctx:      ctx,
                stats:    stats,
        }
        for i := 0; i < n; i++ {
                wp.wg.Add(1)
                go wp.worker()
        }
        return wp
}

func (wp *workerPool) nextRPC() string {
        idx := wp.rpcIdx.Add(1) - 1
        return wp.rpcList[idx%int64(len(wp.rpcList))]
}

func (wp *workerPool) worker() {
        defer wp.wg.Done()

        for w := range wp.jobs {
                // Periksa context sebelum memproses
                select {
                case <-wp.ctx.Done():
                        return
                default:
                }

                var balWei *big.Int
                var err error

                for attempt := 0; attempt <= wp.retries; attempt++ {
                        // Hentikan retry jika context sudah cancel
                        select {
                        case <-wp.ctx.Done():
                                wp.stats.Errors.Add(1)
                                goto nextWallet
                        default:
                        }

                        if attempt > 0 {
                                select {
                                case <-wp.ctx.Done():
                                        wp.stats.Errors.Add(1)
                                        goto nextWallet
                                case <-time.After(time.Duration(attempt) * 150 * time.Millisecond):
                                }
                        }

                        rpcURL := wp.nextRPC()

                        var client *http.Client
                        if wp.useProxy && wp.pm != nil && wp.pm.Count() > 0 {
                                px := wp.pm.Next()
                                if px != nil {
                                        client = proxy.BuildHTTPClient(px.Address, wp.timeout)
                                } else {
                                        client = proxy.BuildHTTPClient("", wp.timeout)
                                }
                        } else {
                                client = proxy.BuildHTTPClient("", wp.timeout)
                        }

                        // Gunakan ctx utama agar langsung cancel saat shutdown
                        reqCtx, cancel := context.WithTimeout(wp.ctx, wp.timeout)
                        balWei, err = getBalance(reqCtx, client, rpcURL, w.Address)
                        cancel()

                        if err == nil {
                                break
                        }
                }

                wp.stats.Checked.Add(1)
                if err != nil {
                        wp.stats.Errors.Add(1)
                        goto nextWallet
                }

                if balWei.Cmp(big.NewInt(0)) > 0 {
                        wp.stats.Found.Add(1)
                        select {
                        case wp.results <- HuntResult{w: w, balance: weiToEth(balWei)}:
                        case <-wp.ctx.Done():
                                return
                        }
                }

        nextWallet:
        }
}

func (wp *workerPool) close() {
        close(wp.jobs)
        wp.wg.Wait()
        close(wp.results)
}

// ─── Found Writer ─────────────────────────────────────────────────────────────

type foundWriter struct {
        mu   sync.Mutex
        file *os.File
        buf  *bufio.Writer
}

func newFoundWriter(path string) (*foundWriter, error) {
        f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
        if err != nil {
                return nil, err
        }
        return &foundWriter{file: f, buf: bufio.NewWriter(f)}, nil
}

func (fw *foundWriter) write(r HuntResult) {
        fw.mu.Lock()
        defer fw.mu.Unlock()
        balF, _ := r.balance.Float64()
        fmt.Fprintf(fw.buf, "ADDRESS=%s | PRIVKEY=0x%s | BALANCE=%.8f ETH\n",
                r.w.Address, r.w.PrivateKey, balF)
        fw.buf.Flush()
}

func (fw *foundWriter) close() {
        fw.mu.Lock()
        defer fw.mu.Unlock()
        fw.buf.Flush()
        fw.file.Close()
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
        workers := flag.Int("workers", runtime.NumCPU()*3, "jumlah worker concurrent")
        rpcURLs := flag.String("rpc", "", "comma-separated RPC URLs (default: 5 public RPCs)")
        useProxyFlag := flag.Bool("proxy", false, "gunakan proxy dari proxies.txt")
        proxyFile := flag.String("pfile", "proxies.txt", "path file proxy")
        fetchProxy := flag.Bool("fetch-proxy", false, "fetch & validasi proxy baru dari internet sekarang")
        validateWorkers := flag.Int("proxy-workers", 50, "jumlah worker validasi proxy")
        timeout := flag.Duration("timeout", 12*time.Second, "timeout per RPC request")
        retries := flag.Int("retries", 3, "retry per wallet check")
        outputFile := flag.String("o", "found.txt", "file output wallet yang punya balance")
        statsInterval := flag.Duration("stats", 5*time.Second, "interval tampilkan statistik")
        flag.Parse()

        printBanner()

        // ── RPC list ──
        var rpcList []string
        if *rpcURLs != "" {
                for _, u := range strings.Split(*rpcURLs, ",") {
                        u = strings.TrimSpace(u)
                        if u != "" {
                                rpcList = append(rpcList, u)
                        }
                }
        } else {
                rpcList = defaultRPCs
        }

        // ── Proxy Manager ──
        pm := proxy.NewManager(*proxyFile, 3)
        pm.OnRefetch = func(count int) {
                cGreen.Printf("\n[PROXY] +%d proxy baru berhasil divalidasi dan disimpan\n", count)
        }
        pm.OnRemove = func(addr string) {
                cDim.Printf("[PROXY] Proxy mati dibuang: %s\n", addr)
        }

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        if *fetchProxy || *useProxyFlag {
                if err := pm.Load(); err != nil {
                        cRed.Printf("[PROXY] Gagal load file: %v\n", err)
                }
                if *fetchProxy || pm.Count() == 0 {
                        cYellow.Printf("[PROXY] Mengambil & memvalidasi proxy dari %d sumber...\n", len(proxy.ProxySources))
                        for _, src := range proxy.ProxySources {
                                cDim.Printf("        → %s\n", src)
                        }
                        pm.FetchAndValidate(ctx, *validateWorkers)
                        cGreen.Printf("[PROXY] Total proxy valid: %d\n", pm.Count())
                } else {
                        cGreen.Printf("[PROXY] Loaded %d proxy dari %s\n", pm.Count(), *proxyFile)
                }
                go pm.AutoRefresh(ctx, 10, 2*time.Minute)
        }

        // ── Output file ──
        absOut, _ := filepath.Abs(*outputFile)
        fw, err := newFoundWriter(*outputFile)
        if err != nil {
                cRed.Printf("[ERROR] Gagal buat output file: %v\n", err)
                os.Exit(1)
        }
        defer fw.close()

        // ── Print config ──
        cYellow.Printf("\n[CONFIG] Workers:   %d\n", *workers)
        cYellow.Printf("[CONFIG] RPC Nodes: %d endpoint\n", len(rpcList))
        for i, r := range rpcList {
                cDim.Printf("         [%d] %s\n", i+1, r)
        }
        if *useProxyFlag {
                cYellow.Printf("[CONFIG] Proxy:     ON (%d aktif dari %s)\n", pm.Count(), *proxyFile)
        } else {
                cYellow.Printf("[CONFIG] Proxy:     OFF\n")
        }
        cYellow.Printf("[CONFIG] Output:    %s\n", absOut)
        cYellow.Printf("[CONFIG] Timeout:   %s | Retries: %d\n", *timeout, *retries)
        cGreen.Printf("\n[START] Hunting dimulai... (Ctrl+C untuk berhenti)\n\n")

        // ── Init stats & pool ──
        stats := &Stats{startTime: time.Now()}
        pool := newWorkerPool(ctx, *workers, rpcList, pm, *useProxyFlag, *timeout, *retries, stats)

        // ── Signal handler ──
        sig := make(chan os.Signal, 1)
        signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

        // ── Stats printer ──
        go func() {
                ticker := time.NewTicker(*statsInterval)
                defer ticker.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-ticker.C:
                                elapsed := time.Since(stats.startTime).Round(time.Second)
                                proxyInfo := ""
                                if *useProxyFlag {
                                        proxyInfo = fmt.Sprintf(" | Proxy: %d aktif", pm.Count())
                                }
                                cDim.Printf("\r[%s] Gen: %d (%.0f/s) | Chk: %d (%.0f/s) | Found: %d | Err: %d%s   ",
                                        elapsed,
                                        stats.Generated.Load(), stats.genRate(),
                                        stats.Checked.Load(), stats.checkRate(),
                                        stats.Found.Load(),
                                        stats.Errors.Load(),
                                        proxyInfo,
                                )
                        }
                }
        }()

        // ── Result printer ──
        go func() {
                for r := range pool.results {
                        balF, _ := r.balance.Float64()
                        fmt.Println()
                        cMagenta.Println("╔══════════════════════════════════════════════════╗")
                        cMagenta.Println("║       💰 WALLET DENGAN BALANCE DITEMUKAN!        ║")
                        cMagenta.Println("╚══════════════════════════════════════════════════╝")
                        cWhite.Printf("  Address:  %s\n", r.w.Address)
                        cGreen.Printf("  Balance:  %.8f ETH\n", balF)
                        cYellow.Printf("  PrivKey:  0x%s\n", r.w.PrivateKey)
                        cDim.Printf("  Disimpan ke: %s\n", absOut)
                        fmt.Println()
                        fw.write(r)
                }
        }()

        // ── Generator → worker jobs ──
        go func() {
                for {
                        select {
                        case <-ctx.Done():
                                return
                        default:
                        }
                        w, err := wallet.Generate()
                        if err != nil {
                                continue
                        }
                        stats.Generated.Add(1)
                        select {
                        case pool.jobs <- w:
                        case <-ctx.Done():
                                return
                        }
                }
        }()

        // ── Tunggu sinyal stop ──
        <-sig
        cancel()

        cYellow.Println("\n\n[STOP] Menghentikan... tunggu sebentar")
        pool.close()

        elapsed := time.Since(stats.startTime).Round(time.Millisecond)

        fmt.Println()
        cCyan.Println("════════════════════════════════════════════════════")
        cWhite.Printf("  Total Generated:   %d wallets\n", stats.Generated.Load())
        cWhite.Printf("  Total Checked:     %d wallets\n", stats.Checked.Load())
        cGreen.Printf("  Found (balance>0): %d wallets\n", stats.Found.Load())
        cRed.Printf("  Errors:            %d\n", stats.Errors.Load())
        cYellow.Printf("  Gen Speed:         %.0f wallet/sec\n", stats.genRate())
        cYellow.Printf("  Check Speed:       %.0f wallet/sec\n", stats.checkRate())
        cCyan.Printf("  Total Time:        %s\n", elapsed)
        if stats.Found.Load() > 0 {
                cGreen.Printf("  📁 Hasil di:       %s\n", absOut)
        }
        cCyan.Println("════════════════════════════════════════════════════")
        fmt.Println()

        // Simpan proxy aktif ke file
        if *useProxyFlag && pm.Count() > 0 {
                pm.Save()
                cGreen.Printf("[PROXY] %d proxy aktif disimpan ke %s\n\n", pm.Count(), *proxyFile)
        }
}
