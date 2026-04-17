package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"

	"eth-wallet-tool/internal/config"
	"eth-wallet-tool/internal/mnemonic"
	"eth-wallet-tool/internal/wallet"
)

var (
	boldCyan    = color.New(color.FgCyan, color.Bold)
	boldGreen   = color.New(color.FgGreen, color.Bold)
	boldYellow  = color.New(color.FgYellow, color.Bold)
	boldMagenta = color.New(color.FgMagenta, color.Bold)
	boldRed     = color.New(color.FgRed, color.Bold)
	dimWhite    = color.New(color.FgWhite, color.Faint)
)

const chanBufCap = 4096

func printBanner() {
	boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET GENERATOR  v2.0                ║
║    High-Speed Ethereum Wallet Generator           ║
╚═══════════════════════════════════════════════════╝`)
}

type genResult struct {
	idx        int
	address    string
	privateKey string
	publicKey  string
	mnemonic   string
	path       string
	err        error
}

func main() {
	cfgFile := flag.String("config", "config.yaml", "path file konfigurasi")
	count := flag.Int("n", 10, "jumlah wallet yang digenerate")
	workers := flag.Int("workers", 0, "jumlah worker (0=auto)")
	output := flag.String("o", "", "output file (default: stdout)")
	showPriv := flag.Bool("priv", true, "tampilkan private key")
	showPub := flag.Bool("pub", false, "tampilkan public key")
	showMnem := flag.Bool("mnem", false, "tampilkan mnemonic (mode mnemonic)")
	csvMode := flag.Bool("csv", false, "output format CSV")
	modeFlag := flag.String("mode", "", "random atau mnemonic (override config)")
	flag.Parse()

	printBanner()

	// ── Load config ──
	cfg, err := config.Load(*cfgFile)
	if err != nil {
		boldRed.Printf("[WARN] Config: %v — pakai default\n", err)
		cfg = config.Default()
	}

	// Override dari flag
	mode := cfg.Generator.Mode
	if *modeFlag != "" {
		mode = *modeFlag
	}
	mnemonicWords := cfg.Generator.MnemonicWords

	// Worker count
	nWorkers := *workers
	if nWorkers <= 0 {
		nWorkers = cfg.Workers.Generator
	}
	if nWorkers <= 0 {
		nWorkers = runtime.NumCPU()
	}

	boldYellow.Printf("\n[CONFIG] Mode:     %s\n", strings.ToUpper(mode))
	boldYellow.Printf("[CONFIG] Generate: %d wallet | Workers: %d\n\n", *count, nWorkers)

	// ── Setup output file ──
	var writer *bufio.Writer
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			boldRed.Printf("[ERROR] Gagal buat output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		writer = bufio.NewWriterSize(f, 1<<20)
		defer writer.Flush()
		boldGreen.Printf("[OUTPUT] Menyimpan ke: %s\n\n", *output)
	}

	// ── Progress bar ──
	var generated atomic.Int64
	var progressDone = make(chan struct{})
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		start := time.Now()
		barWidth := 30
		for {
			select {
			case <-ticker.C:
				cur := generated.Load()
				pct := float64(cur) / float64(*count)
				filled := int(pct * float64(barWidth))
				if filled > barWidth {
					filled = barWidth
				}
				bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
				elapsed := time.Since(start).Seconds()
				speed := 0.0
				if elapsed > 0 {
					speed = float64(cur) / elapsed
				}
				fmt.Fprintf(os.Stderr, "\r[%s] %d/%d (%.1f%%) | %.0f wallet/s   ",
					bar, cur, *count, pct*100, speed)
				if cur >= int64(*count) {
					fmt.Fprintln(os.Stderr)
					return
				}
			}
		}
	}()

	// ── CSV header ──
	if *csvMode {
		header := "index,address,private_key"
		if *showPub {
			header += ",public_key"
		}
		if mode == "mnemonic" && *showMnem {
			header += ",mnemonic,path"
		}
		fmt.Println(header)
		if writer != nil {
			fmt.Fprintln(writer, header)
		}
	}

	// ── Worker pool ──
	bufSize := *count
	if bufSize > chanBufCap {
		bufSize = chanBufCap
	}

	jobs := make(chan int, bufSize)
	results := make(chan genResult, bufSize)

	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				var r genResult
				r.idx = idx

				if mode == "mnemonic" {
					w, err := mnemonic.Generate(mnemonicWords, 0)
					if err != nil {
						r.err = err
					} else {
						r.address = w.Address
						r.privateKey = w.PrivateKey
						r.publicKey = w.PublicKey
						r.mnemonic = w.Mnemonic
						r.path = w.Path
					}
				} else {
					w, err := wallet.Generate()
					if err != nil {
						r.err = err
					} else {
						r.address = w.Address
						r.privateKey = w.PrivateKey
						r.publicKey = w.PublicKey
					}
				}
				results <- r
				generated.Add(1)
			}
		}()
	}

	go func() {
		for i := 1; i <= *count; i++ {
			jobs <- i
		}
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	// ── Kumpulkan dan urutkan ──
	start := time.Now()
	sorted := make([]genResult, *count+1)
	var errCount int

	for r := range results {
		if r.idx >= 1 && r.idx <= *count {
			sorted[r.idx] = r
		}
	}

	<-progressDone

	// ── Print hasil ──
	var genCount int
	for i := 1; i <= *count; i++ {
		r := sorted[i]
		if r.err != nil {
			boldRed.Printf("[ERROR] #%d: %v\n", r.idx, r.err)
			errCount++
			continue
		}
		if r.address == "" {
			continue
		}
		genCount++

		if *csvMode {
			line := fmt.Sprintf("%d,%s,%s", r.idx, r.address, r.privateKey)
			if *showPub {
				line += "," + r.publicKey
			}
			if mode == "mnemonic" && *showMnem {
				line += "," + r.mnemonic + "," + r.path
			}
			fmt.Println(line)
			if writer != nil {
				fmt.Fprintln(writer, line)
			}
		} else {
			boldGreen.Printf("[#%04d] ", r.idx)
			fmt.Printf("Address:  ")
			boldCyan.Println(r.address)

			if *showPriv {
				dimWhite.Printf("         PrivKey:  ")
				boldMagenta.Println("0x" + r.privateKey)
			}
			if *showPub {
				dimWhite.Printf("         PubKey:   ")
				fmt.Println("0x" + r.publicKey[:32] + "...")
			}
			if mode == "mnemonic" && *showMnem {
				dimWhite.Printf("         Mnemonic: ")
				boldYellow.Println(r.mnemonic)
				dimWhite.Printf("         Path:     %s\n", r.path)
			}
			dimWhite.Println("         ──────────────────────────────────────────────")

			if writer != nil {
				line := fmt.Sprintf("[#%04d] Address: %s | PrivKey: 0x%s", r.idx, r.address, r.privateKey)
				if mode == "mnemonic" && *showMnem {
					line += " | Mnemonic: " + r.mnemonic
				}
				fmt.Fprintln(writer, line)
			}
		}
	}

	elapsed := time.Since(start)
	speed := float64(genCount) / elapsed.Seconds()

	fmt.Println()
	boldGreen.Printf("✓ Generated:  %d wallets\n", genCount)
	if errCount > 0 {
		boldRed.Printf("✗ Errors:     %d\n", errCount)
	}
	boldYellow.Printf("⚡ Speed:      %.0f wallets/sec\n", speed)
	boldCyan.Printf("⏱  Time:       %s\n", elapsed.Round(time.Millisecond))
	if *output != "" {
		boldGreen.Printf("💾 Saved to:   %s\n", *output)
	}
	fmt.Println()
}
