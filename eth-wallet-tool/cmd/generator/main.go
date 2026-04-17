package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/fatih/color"

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

const chanBufCap = 4096 // batas buffer channel agar tidak habiskan RAM

func printBanner() {
	boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET GENERATOR  v1.0                ║
║    High-Speed Ethereum Wallet Generator           ║
╚═══════════════════════════════════════════════════╝`)
}

func main() {
	count := flag.Int("n", 10, "jumlah wallet yang digenerate")
	workers := flag.Int("workers", runtime.NumCPU(), "jumlah worker concurrent")
	output := flag.String("o", "", "output file (default: stdout)")
	showPriv := flag.Bool("priv", true, "tampilkan private key")
	showPub := flag.Bool("pub", false, "tampilkan public key")
	csvMode := flag.Bool("csv", false, "output format CSV")
	flag.Parse()

	printBanner()

	boldYellow.Printf("\n[CONFIG] Generating %d wallets dengan %d workers\n\n", *count, *workers)

	// ── Setup output file ──
	var writer *bufio.Writer
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			boldRed.Printf("[ERROR] Gagal buat output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		writer = bufio.NewWriterSize(f, 1<<20) // 1 MB buffer
		defer writer.Flush()
		boldGreen.Printf("[OUTPUT] Menyimpan ke: %s\n\n", *output)
	}

	type result struct {
		idx int
		w   *wallet.Wallet
		err error
	}

	// Buffer dibatasi agar tidak habiskan RAM untuk count besar
	bufSize := *count
	if bufSize > chanBufCap {
		bufSize = chanBufCap
	}

	jobs := make(chan int, bufSize)
	results := make(chan result, bufSize)

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				w, err := wallet.Generate()
				results <- result{idx: idx, w: w, err: err}
			}
		}()
	}

	// Feed jobs ke workers
	go func() {
		for i := 1; i <= *count; i++ {
			jobs <- i
		}
		close(jobs)
	}()

	// Tutup results setelah semua worker selesai
	go func() {
		wg.Wait()
		close(results)
	}()

	// CSV header
	if *csvMode {
		header := "index,address,private_key"
		if *showPub {
			header += ",public_key"
		}
		fmt.Println(header)
		if writer != nil {
			fmt.Fprintln(writer, header)
		}
	}

	start := time.Now()
	var generated, errCount int

	// Kumpulkan dan urutkan hasil (order konsisten)
	collected := make([]result, 0, min(*count, chanBufCap))
	for r := range results {
		collected = append(collected, r)
	}

	sorted := make([]result, *count)
	for _, r := range collected {
		if r.idx >= 1 && r.idx <= *count {
			sorted[r.idx-1] = r
		}
	}

	for _, r := range sorted {
		// Slot kosong (seharusnya tidak terjadi, guard saja)
		if r.w == nil && r.err == nil {
			continue
		}
		if r.err != nil {
			boldRed.Printf("[ERROR] #%d: %v\n", r.idx, r.err)
			errCount++
			continue
		}
		generated++

		if *csvMode {
			line := fmt.Sprintf("%d,%s,%s", r.idx, r.w.Address, r.w.PrivateKey)
			if *showPub {
				line += "," + r.w.PublicKey
			}
			fmt.Println(line)
			if writer != nil {
				fmt.Fprintln(writer, line)
			}
		} else {
			boldGreen.Printf("[#%04d] ", r.idx)
			fmt.Printf("Address:  ")
			boldCyan.Println(r.w.Address)

			if *showPriv {
				dimWhite.Printf("         PrivKey:  ")
				boldMagenta.Println("0x" + r.w.PrivateKey)
			}
			if *showPub {
				dimWhite.Printf("         PubKey:   ")
				fmt.Println("0x" + r.w.PublicKey[:32] + "...")
			}
			dimWhite.Println("         ──────────────────────────────────────────────")

			if writer != nil {
				line := fmt.Sprintf("[#%04d] Address: %s | PrivKey: 0x%s", r.idx, r.w.Address, r.w.PrivateKey)
				if *showPub {
					line += " | PubKey: 0x" + r.w.PublicKey
				}
				fmt.Fprintln(writer, line)
			}
		}
	}

	elapsed := time.Since(start)
	speed := float64(generated) / elapsed.Seconds()

	fmt.Println()
	boldGreen.Printf("✓ Generated:  %d wallets\n", generated)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
