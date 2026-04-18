// eth-hunt: Auto generate + check wallet Ethereum
package main

import (
        "bufio"
        "context"
        "encoding/json"
        "flag"
        "fmt"
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

        "eth-wallet-tool/internal/config"
        "eth-wallet-tool/internal/mnemonic"
        "eth-wallet-tool/internal/notify"
        "eth-wallet-tool/internal/rpc"
        "eth-wallet-tool/internal/wallet"
)

var (
        cCyan    = color.New(color.FgCyan, color.Bold)
        cGreen   = color.New(color.FgGreen, color.Bold)
        cYellow  = color.New(color.FgYellow, color.Bold)
        cRed     = color.New(color.FgRed, color.Bold)
        cWhite   = color.New(color.FgWhite, color.Bold)
        cDim     = color.New(color.FgWhite, color.Faint)
        cMagenta = color.New(color.FgMagenta, color.Bold)
)

func printBanner() {
        cCyan.Print(`
╔════════════════════════════════════════════════════════╗
║           ETH WALLET HUNTER  v4.0                      ║
║        Auto Generate + Check | Ethereum Mainnet        ║
╚════════════════════════════════════════════════════════╝
`)
}

// ─── Resume State ─────────────────────────────────────────────────────────────

type ResumeState struct {
        Generated int64 `json:"generated"`
        Checked   int64 `json:"checked"`
        Found     int64 `json:"found"`
        Sessions  int64 `json:"sessions"`
}

func loadResume(path string) *ResumeState {
        f, err := os.Open(path)
        if err != nil {
                return &ResumeState{}
        }
        defer f.Close()
        var s ResumeState
        if err := json.NewDecoder(f).Decode(&s); err != nil {
                return &ResumeState{}
        }
        return &s
}

func saveResume(path string, s *ResumeState) {
        f, err := os.Create(path)
        if err != nil {
                return
        }
        defer f.Close()
        json.NewEncoder(f).Encode(s)
}

// ─── Stats Logger (CSV) ───────────────────────────────────────────────────────

type statsLogger struct {
        mu   sync.Mutex
        file *os.File
        buf  *bufio.Writer
}

func newStatsLogger(path string) *statsLogger {
        if path == "" {
                return nil
        }
        needHeader := true
        if _, err := os.Stat(path); err == nil {
                needHeader = false
        }
        f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
        if err != nil {
                return nil
        }
        sl := &statsLogger{file: f, buf: bufio.NewWriter(f)}
        if needHeader {
                fmt.Fprintln(sl.buf, "timestamp,generated,checked,found,errors,gen_rate,check_rate,rpc_alive")
                sl.buf.Flush()
        }
        return sl
}

func (sl *statsLogger) write(generated, checked, found, errors int64, genRate, checkRate float64, rpcAlive int) {
        if sl == nil {
                return
        }
        sl.mu.Lock()
        defer sl.mu.Unlock()
        fmt.Fprintf(sl.buf, "%s,%d,%d,%d,%d,%.2f,%.2f,%d\n",
                time.Now().Format(time.RFC3339),
                generated, checked, found, errors, genRate, checkRate, rpcAlive)
        sl.buf.Flush()
}

func (sl *statsLogger) close() {
        if sl == nil {
                return
        }
        sl.mu.Lock()
        defer sl.mu.Unlock()
        sl.buf.Flush()
        sl.file.Close()
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
        el := time.Since(s.startTime).Seconds()
        if el == 0 {
                return 0
        }
        return float64(s.Generated.Load()) / el
}

func (s *Stats) checkRate() float64 {
        el := time.Since(s.startTime).Seconds()
        if el == 0 {
                return 0
        }
        return float64(s.Checked.Load()) / el
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

func (fw *foundWriter) write(address, privKey, mnem string, ethBal float64, tokens map[string]float64) {
        fw.mu.Lock()
        defer fw.mu.Unlock()
        line := fmt.Sprintf("ADDRESS=%s | PRIVKEY=0x%s | BALANCE=%.8f ETH",
                address, privKey, ethBal)
        for name, bal := range tokens {
                if bal > 0 {
                        line += fmt.Sprintf(" | %s=%.6f", name, bal)
                }
        }
        if mnem != "" {
                line += " | MNEMONIC=" + mnem
        }
        fmt.Fprintln(fw.buf, line)
        fw.buf.Flush()
}

func (fw *foundWriter) close() {
        fw.mu.Lock()
        defer fw.mu.Unlock()
        fw.buf.Flush()
        fw.file.Close()
}

// ─── Worker Pool ─────────────────────────────────────────────────────────────

type walletEntry struct {
        address    string
        privateKey string
        mnemonic   string
}

type batchJob struct {
        wallets []walletEntry
}

type workerPool struct {
        jobs    chan batchJob
        results chan foundEntry
        rpcMgr  *rpc.Manager
        tokens  []rpc.TokenCheck
        ctx     context.Context
        wg      sync.WaitGroup
        stats   *Stats
}

type foundEntry struct {
        address    string
        privateKey string
        mnemonic   string
        ethWei     *big.Int
        tokenBals  map[string]*big.Int
}

func newWorkerPool(ctx context.Context, n int, rpcMgr *rpc.Manager, tokens []rpc.TokenCheck, stats *Stats) *workerPool {
        wp := &workerPool{
                jobs:    make(chan batchJob, n*2),
                results: make(chan foundEntry, n),
                rpcMgr:  rpcMgr,
                tokens:  tokens,
                ctx:     ctx,
                stats:   stats,
        }
        for i := 0; i < n; i++ {
                wp.wg.Add(1)
                go wp.worker()
        }
        return wp
}

func (wp *workerPool) worker() {
        defer wp.wg.Done()

        for job := range wp.jobs {
                select {
                case <-wp.ctx.Done():
                        return
                default:
                }

                addresses := make([]string, len(job.wallets))
                for i, w := range job.wallets {
                        addresses[i] = w.address
                }

                batchRes, err := wp.rpcMgr.GetBalanceBatch(wp.ctx, addresses, wp.tokens)
                wp.stats.Checked.Add(int64(len(addresses)))

                if err != nil {
                        wp.stats.Errors.Add(int64(len(addresses)))
                        continue
                }

                for _, w := range job.wallets {
                        ar, ok := batchRes[w.address]
                        if !ok {
                                continue
                        }
                        if rpc.HasAnyBalance(ar) {
                                tBals := make(map[string]*big.Int)
                                for k, v := range ar.Tokens {
                                        tBals[k] = v
                                }
                                select {
                                case wp.results <- foundEntry{
                                        address:    w.address,
                                        privateKey: w.privateKey,
                                        mnemonic:   w.mnemonic,
                                        ethWei:     ar.ETH,
                                        tokenBals:  tBals,
                                }:
                                case <-wp.ctx.Done():
                                        return
                                }
                        }
                }
        }
}

func (wp *workerPool) close() {
        close(wp.jobs)
        wp.wg.Wait()
        close(wp.results)
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
        cfgFile := flag.String("config", "config.yaml", "path file konfigurasi")
        workers := flag.Int("workers", 0, "jumlah worker (0=auto dari config)")
        rpcURLs := flag.String("rpc", "", "comma-separated RPC URLs (override config)")
        outputFile := flag.String("o", "", "file output (override config)")
        statsInterval := flag.Duration("stats", 0, "interval statistik (override config)")
        modeFlag := flag.String("mode", "", "random atau mnemonic (override config)")
        testTelegram := flag.Bool("test-telegram", false, "test kirim pesan Telegram")
        flag.Parse()

        printBanner()

        cfg, err := config.Load(*cfgFile)
        if err != nil {
                cRed.Printf("[WARN] Config: %v — pakai default\n", err)
                cfg = config.Default()
        }

        genMode := cfg.Generator.Mode
        if *modeFlag != "" {
                genMode = *modeFlag
        }
        foundFile := cfg.Output.FoundFile
        if *outputFile != "" {
                foundFile = *outputFile
        }
        statInterval := cfg.Hunt.StatsInterval
        if *statsInterval > 0 {
                statInterval = *statsInterval
        }

        nWorkers := *workers
        if nWorkers <= 0 {
                nWorkers = cfg.Workers.Hunt
        }
        if nWorkers <= 0 {
                nWorkers = runtime.NumCPU() * 3
        }

        // ── Telegram ──
        tg := notify.NewTelegram(notify.TelegramConfig{
                Enabled: cfg.Telegram.Enabled,
                Token:   cfg.Telegram.Token,
                ChatID:  cfg.Telegram.ChatID,
        })

        if *testTelegram {
                cYellow.Println("[TELEGRAM] Mengirim pesan test...")
                if err := tg.SendTest(); err != nil {
                        cRed.Printf("[TELEGRAM] GAGAL: %v\n", err)
                } else {
                        cGreen.Println("[TELEGRAM] Berhasil! Cek Telegram kamu.")
                }
                return
        }

        // ── RPC Endpoints ──
        endpoints := cfg.RPC.Endpoints
        if *rpcURLs != "" {
                endpoints = nil
                for _, u := range strings.Split(*rpcURLs, ",") {
                        u = strings.TrimSpace(u)
                        if u != "" {
                                endpoints = append(endpoints, u)
                        }
                }
        }
        if len(endpoints) == 0 {
                cRed.Println("[ERROR] Tidak ada RPC endpoint dikonfigurasi")
                os.Exit(1)
        }

        // ── Token checks ──
        var tokenChecks []rpc.TokenCheck
        if cfg.Tokens.CheckERC20 {
                for _, t := range cfg.Tokens.List {
                        tokenChecks = append(tokenChecks, rpc.TokenCheck{
                                Name: t.Name, Address: t.Address, Decimals: t.Decimals,
                        })
                }
        }

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        tg.Start(ctx)

        // ── RPC Manager ──
        httpClient := &http.Client{
                Timeout: cfg.RPC.Timeout,
                Transport: &http.Transport{
                        MaxIdleConns:        200,
                        MaxIdleConnsPerHost: 50,
                        IdleConnTimeout:     60 * time.Second,
                },
        }
        rpcMgr := rpc.NewManager(endpoints, httpClient,
                cfg.RPC.Timeout, cfg.RPC.Retries,
                cfg.RPC.DeadThreshold, cfg.RPC.DeadCooldown,
                cfg.RPC.RateLimit)

        // ── Resume State ──
        resume := loadResume(cfg.Output.ResumeFile)
        resume.Sessions++

        // ── Found Writer ──
        absOut, _ := filepath.Abs(foundFile)
        fw, err := newFoundWriter(foundFile)
        if err != nil {
                cRed.Printf("[ERROR] Gagal buat output file: %v\n", err)
                os.Exit(1)
        }
        defer fw.close()

        // ── Stats Logger ──
        sl := newStatsLogger(cfg.Output.StatsLog)
        defer sl.close()

        // ── Print config ──
        cYellow.Printf("\n[CONFIG] Network:    Ethereum Mainnet (ETH)\n")
        cYellow.Printf("[CONFIG] Mode:       %s\n", strings.ToUpper(genMode))
        cYellow.Printf("[CONFIG] Workers:    %d | Batch: %d\n", nWorkers, cfg.RPC.BatchSize)
        cYellow.Printf("[CONFIG] RPC Nodes:  %d endpoint\n", len(endpoints))
        for i, ep := range endpoints {
                cDim.Printf("         [%d] %s\n", i+1, ep)
        }
        if len(tokenChecks) > 0 {
                names := make([]string, len(tokenChecks))
                for i, t := range tokenChecks {
                        names[i] = t.Name
                }
                cYellow.Printf("[CONFIG] Tokens:     %s\n", strings.Join(names, ", "))
        }
        if cfg.Telegram.Enabled {
                cYellow.Printf("[CONFIG] Telegram:   ON (chat_id: %s)\n", cfg.Telegram.ChatID)
        }
        cYellow.Printf("[CONFIG] Output:     %s\n", absOut)
        if cfg.Output.StatsLog != "" {
                cYellow.Printf("[CONFIG] Stats CSV:  %s\n", cfg.Output.StatsLog)
        }
        if resume.Sessions > 1 {
                cGreen.Printf("[RESUME] Sesi ke-%d | Total lalu: Gen=%d Chk=%d Found=%d\n",
                        resume.Sessions, resume.Generated, resume.Checked, resume.Found)
        }
        cGreen.Printf("\n[START] Hunting dimulai... (Ctrl+C untuk berhenti)\n\n")

        // ── Stats & Workers ──
        stats := &Stats{startTime: time.Now()}
        pool := newWorkerPool(ctx, nWorkers, rpcMgr, tokenChecks, stats)

        // ── Signal handler ──
        sig := make(chan os.Signal, 1)
        signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

        // ── Stats printer ──
        go func() {
                ticker := time.NewTicker(statInterval)
                defer ticker.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-ticker.C:
                                elapsed := time.Since(stats.startTime).Round(time.Second)
                                cDim.Printf("\r[%s] Gen: %d (%.0f/s) | Chk: %d (%.0f/s) | Found: %d | Err: %d | RPC: %d/%d   ",
                                        elapsed,
                                        stats.Generated.Load(), stats.genRate(),
                                        stats.Checked.Load(), stats.checkRate(),
                                        stats.Found.Load(),
                                        stats.Errors.Load(),
                                        rpcMgr.AliveCount(), len(endpoints),
                                )
                                sl.write(stats.Generated.Load(), stats.Checked.Load(),
                                        stats.Found.Load(), stats.Errors.Load(),
                                        stats.genRate(), stats.checkRate(), rpcMgr.AliveCount())
                        }
                }
        }()

        // ── Token decimals lookup ──
        tokenDecimals := make(map[string]int, len(tokenChecks))
        for _, tc := range tokenChecks {
                tokenDecimals[tc.Name] = tc.Decimals
        }

        // ── Result printer ──
        go func() {
                for entry := range pool.results {
                        ethF, _ := rpc.WeiToDecimal(entry.ethWei, 18).Float64()
                        tokenFloats := map[string]float64{}
                        for k, v := range entry.tokenBals {
                                dec := tokenDecimals[k]
                                if dec == 0 {
                                        dec = 18
                                }
                                f, _ := rpc.WeiToDecimal(v, dec).Float64()
                                tokenFloats[k] = f
                        }

                        fmt.Println()
                        cMagenta.Println("╔══════════════════════════════════════════════════╗")
                        cMagenta.Println("║       💰 WALLET DENGAN BALANCE DITEMUKAN!        ║")
                        cMagenta.Println("╚══════════════════════════════════════════════════╝")
                        cWhite.Printf("  Address:  %s\n", entry.address)
                        cGreen.Printf("  Balance:  %.8f ETH\n", ethF)
                        for name, bal := range tokenFloats {
                                if bal > 0 {
                                        cGreen.Printf("  %s:      %.6f\n", name, bal)
                                }
                        }
                        if entry.mnemonic != "" {
                                cYellow.Printf("  Mnemonic: %s\n", entry.mnemonic)
                        }
                        cWhite.Printf("  PrivKey:  0x%s\n", entry.privateKey)
                        cMagenta.Println("══════════════════════════════════════════════════")

                        stats.Found.Add(1)
                        fw.write(entry.address, entry.privateKey, entry.mnemonic, ethF, tokenFloats)
                        tg.Notify(notify.FormatFound(entry.address, entry.privateKey, ethF, "ETH", tokenFloats))
                }
        }()

        // ── Generator goroutine ──
        batchSize := cfg.RPC.BatchSize
        go func() {
                batch := make([]walletEntry, 0, batchSize)
                for {
                        select {
                        case <-ctx.Done():
                                return
                        default:
                        }

                        var we walletEntry
                        if genMode == "mnemonic" {
                                w, err := mnemonic.Generate(cfg.Generator.MnemonicWords, 0)
                                if err != nil {
                                        continue
                                }
                                we = walletEntry{address: w.Address, privateKey: w.PrivateKey, mnemonic: w.Mnemonic}
                        } else {
                                w, err := wallet.Generate()
                                if err != nil {
                                        continue
                                }
                                we = walletEntry{address: w.Address, privateKey: w.PrivateKey}
                        }
                        stats.Generated.Add(1)
                        batch = append(batch, we)

                        if len(batch) >= batchSize {
                                select {
                                case pool.jobs <- batchJob{wallets: batch}:
                                        batch = make([]walletEntry, 0, batchSize)
                                case <-ctx.Done():
                                        return
                                }
                        }
                }
        }()

        // ── Wait for signal ──
        <-sig
        fmt.Println()
        cYellow.Println("\n[STOP] Menghentikan hunter...")
        cancel()
        pool.close()

        // ── Save resume ──
        resume.Generated += stats.Generated.Load()
        resume.Checked += stats.Checked.Load()
        resume.Found += stats.Found.Load()
        saveResume(cfg.Output.ResumeFile, resume)

        elapsed := time.Since(stats.startTime).Round(time.Second)
        cCyan.Println("\n════════════════════════════════════════════════════")
        cWhite.Printf("  Generated:  %d\n", stats.Generated.Load())
        cWhite.Printf("  Checked:    %d\n", stats.Checked.Load())
        cGreen.Printf("  Found:      %d\n", stats.Found.Load())
        cRed.Printf("  Errors:     %d\n", stats.Errors.Load())
        cYellow.Printf("  Gen Speed:  %.0f wallet/s\n", stats.genRate())
        cYellow.Printf("  Chk Speed:  %.0f addr/s\n", stats.checkRate())
        cCyan.Printf("  Duration:   %s\n", elapsed)
        cCyan.Println("════════════════════════════════════════════════════")
        fmt.Println()
}
