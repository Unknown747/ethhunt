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
║           ETH WALLET HUNTER  v4.2                      ║
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
		fmt.Fprintln(sl.buf, "timestamp,generated,checked,found,errors,gen_rate,check_rate,rpc_alive,batch_size")
		sl.buf.Flush()
	}
	return sl
}

func (sl *statsLogger) write(generated, checked, found, errors int64, genRate, checkRate float64, rpcAlive, batchSize int) {
	if sl == nil {
		return
	}
	sl.mu.Lock()
	defer sl.mu.Unlock()
	fmt.Fprintf(sl.buf, "%s,%d,%d,%d,%d,%.2f,%.2f,%d,%d\n",
		time.Now().Format(time.RFC3339),
		generated, checked, found, errors, genRate, checkRate, rpcAlive, batchSize)
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

// ─── Batch Size Auto-Scaler ───────────────────────────────────────────────────

const (
	batchScaleMin = 5
	batchScaleMax = 200
)

// scaleBatch menyesuaikan batch size berdasarkan error rate per interval.
// Error rate < 20% → naik pelan-pelan; > 60% → turun lebih cepat.
func scaleBatch(cur int64, deltaChk, deltaErr int64) int64 {
	if deltaChk < 10 {
		return cur // sample terlalu sedikit, skip
	}
	errRate := float64(deltaErr) / float64(deltaChk)
	switch {
	case errRate < 0.20 && cur < batchScaleMax:
		cur += 2 // naik perlahan saat RPC sehat
	case errRate > 0.60 && cur > batchScaleMin:
		cur -= 5 // turun lebih cepat saat banyak error
	case errRate > 0.40 && cur > batchScaleMin:
		cur -= 2
	}
	if cur < batchScaleMin {
		cur = batchScaleMin
	}
	if cur > batchScaleMax {
		cur = batchScaleMax
	}
	return cur
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
	silent := flag.Bool("silent", false, "sembunyikan semua output kecuali wallet ditemukan")
	flag.Parse()

	if !*silent {
		printBanner()
	}

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
	mnemonicPaths := cfg.Generator.MnemonicPaths

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
	rpcMgr := rpc.NewManager(specs, httpClient,
		cfg.RPC.Timeout, cfg.RPC.Retries,
		cfg.RPC.DeadThreshold, cfg.RPC.DeadCooldown,
		cfg.RPC.RateLimit)

	// ── RPC Health Check ──
	if !*silent {
		cYellow.Printf("[RPC] Mengecek %d endpoint...\n", len(specs))
	}
	hcResults := rpcMgr.HealthCheck(5 * time.Second)
	aliveCount := 0
	for _, s := range hcResults {
		if s.Alive {
			aliveCount++
			if !*silent {
				cGreen.Printf("  ✓ %-50s  %s\n", s.URL, s.Latency.Round(time.Millisecond))
			}
		} else {
			if !*silent {
				cRed.Printf("  ✗ %-50s  TIMEOUT/ERROR\n", s.URL)
			}
		}
	}
	if aliveCount == 0 {
		cRed.Println("[ERROR] Semua RPC endpoint tidak merespons. Periksa koneksi atau tambah endpoint baru di config.yaml")
		os.Exit(1)
	}
	if !*silent {
		cYellow.Printf("[RPC] %d/%d endpoint aktif\n\n", aliveCount, len(specs))
	}

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
	if !*silent {
		cYellow.Printf("[CONFIG] Network:    Ethereum Mainnet (ETH)\n")
		cYellow.Printf("[CONFIG] Mode:       %s", strings.ToUpper(genMode))
		if genMode == "mnemonic" {
			cYellow.Printf("  (seed → %d address per mnemonic)", mnemonicPaths)
		}
		fmt.Println()
		cYellow.Printf("[CONFIG] Workers:    %d | Batch: %d (auto-scale %d–%d)\n",
			nWorkers, cfg.RPC.BatchSize, batchScaleMin, batchScaleMax)
		cYellow.Printf("[CONFIG] RPC Nodes:  %d/%d aktif (latency-priority routing)\n", aliveCount, len(specs))
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
	}

	// ── Stats & Workers ──
	stats := &Stats{startTime: time.Now()}
	pool := newWorkerPool(ctx, nWorkers, rpcMgr, tokenChecks, stats)

	// ── Signal handler ──
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	// ── Auto-scale batch size (atomic, dibaca oleh generator goroutine) ──
	curBatchSize := atomic.Int64{}
	curBatchSize.Store(int64(cfg.RPC.BatchSize))

	// ── Stats printer + Watchdog + Periodic RPC re-check + Telegram alert ──
	var prevChecked, prevErrors int64
	rpcWasAlive := aliveCount > 0
	recheckTicker := time.NewTicker(5 * time.Minute)
	defer recheckTicker.Stop()

	go func() {
		statsTicker := time.NewTicker(statInterval)
		defer statsTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return

			case <-statsTicker.C:
				curChk := stats.Checked.Load()
				curErr := stats.Errors.Load()
				deltaChk := curChk - prevChecked
				deltaErr := curErr - prevErrors
				prevChecked = curChk
				prevErrors = curErr
				bsz := int(curBatchSize.Load())

				if !*silent {
					elapsed := time.Since(stats.startTime).Round(time.Second)
					cDim.Printf("\r[%s] Gen: %d (%.0f/s) | Chk: %d (%.0f/s) | Found: %d | Err: %d | RPC: %d/%d | Batch: %d   ",
						elapsed,
						stats.Generated.Load(), stats.genRate(),
						stats.Checked.Load(), stats.checkRate(),
						stats.Found.Load(),
						stats.Errors.Load(),
						rpcMgr.AliveCount(), rpcMgr.TotalCount(), bsz,
					)
				}
				sl.write(stats.Generated.Load(), stats.Checked.Load(),
					stats.Found.Load(), stats.Errors.Load(),
					stats.genRate(), stats.checkRate(), rpcMgr.AliveCount(), bsz)

				// ── [FITUR 4] Auto-scale batch size ──
				newBsz := scaleBatch(int64(bsz), deltaChk, deltaErr)
				if newBsz != int64(bsz) {
					curBatchSize.Store(newBsz)
					if !*silent {
						fmt.Println()
						if newBsz > int64(bsz) {
							cGreen.Printf("[BATCH] RPC sehat → batch naik: %d → %d\n", bsz, newBsz)
						} else {
							cYellow.Printf("[BATCH] Error tinggi → batch turun: %d → %d\n", bsz, newBsz)
						}
					}
				}

				// ── [FITUR 3] Watchdog: error rate > 70% → re-check RPC ──
				if deltaChk > 0 && float64(deltaErr)/float64(deltaChk) > 0.7 {
					if !*silent {
						fmt.Println()
						cRed.Printf("[WATCHDOG] Error rate %.0f%% → memeriksa ulang RPC...\n",
							float64(deltaErr)/float64(deltaChk)*100)
					}
					revived, total := rpcMgr.ReCheckDead(5 * time.Second)
					if !*silent && total > 0 {
						cYellow.Printf("[WATCHDOG] %d/%d endpoint mati kembali aktif\n", revived, total)
					}
				}

				// ── [FITUR 3] Telegram alert: semua RPC mati / pulih ──
				aliveNow := rpcMgr.AliveCount() > 0
				if !aliveNow && rpcWasAlive {
					rpcWasAlive = false
					tg.Notify("⚠️ ETH Hunt: SEMUA RPC endpoint mati! Tool berjalan tanpa koneksi aktif.")
				} else if aliveNow && !rpcWasAlive {
					rpcWasAlive = true
					tg.Notify(fmt.Sprintf("✅ ETH Hunt: RPC kembali normal. %d/%d endpoint aktif.",
						rpcMgr.AliveCount(), rpcMgr.TotalCount()))
				}

			case <-recheckTicker.C:
				// ── [FITUR 1] Periodic re-check tiap 5 menit ──
				revived, total := rpcMgr.ReCheckDead(5 * time.Second)
				if !*silent && total > 0 {
					fmt.Println()
					cYellow.Printf("[RPC] Periodic re-check: %d/%d endpoint mati kembali aktif | Aktif: %d/%d\n",
						revived, total, rpcMgr.AliveCount(), rpcMgr.TotalCount())
				}
			}
		}
	}()

	// ── Token decimals lookup ──
	tokenDecimals := make(map[string]int, len(tokenChecks))
	for _, tc := range tokenChecks {
		tokenDecimals[tc.Name] = tc.Decimals
	}

	// ── Result printer (selalu tampil meski -silent) ──
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
	// [FITUR 1] Multi-derivation path: mode mnemonic → 1 seed menghasilkan N address
	go func() {
		batch := make([]walletEntry, 0, int(curBatchSize.Load()))
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			bsz := int(curBatchSize.Load())

			if genMode == "mnemonic" {
				// Hasilkan 1 seed, derive mnemonicPaths address dari seed itu
				baseWallet, err := mnemonic.Generate(cfg.Generator.MnemonicWords, 0)
				if err != nil {
					continue
				}
				for idx := uint32(0); idx < uint32(mnemonicPaths); idx++ {
					select {
					case <-ctx.Done():
						return
					default:
					}
					w, err := mnemonic.FromMnemonic(baseWallet.Mnemonic, idx)
					if err != nil {
						continue
					}
					stats.Generated.Add(1)
					batch = append(batch, walletEntry{
						address:    w.Address,
						privateKey: w.PrivateKey,
						mnemonic:   w.Mnemonic,
					})

					if len(batch) >= bsz {
						select {
						case pool.jobs <- batchJob{wallets: batch}:
							batch = make([]walletEntry, 0, bsz)
						case <-ctx.Done():
							return
						}
					}
				}
			} else {
				w, err := wallet.Generate()
				if err != nil {
					continue
				}
				stats.Generated.Add(1)
				batch = append(batch, walletEntry{address: w.Address, privateKey: w.PrivateKey})

				if len(batch) >= bsz {
					select {
					case pool.jobs <- batchJob{wallets: batch}:
						batch = make([]walletEntry, 0, bsz)
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// ── Wait for signal ──
	<-sig
	fmt.Println()
	if !*silent {
		cYellow.Println("\n[STOP] Menghentikan hunter...")
	}
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
