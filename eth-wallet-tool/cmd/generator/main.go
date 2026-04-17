package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
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

func printBanner() {
	boldCyan.Println(`
╔═══════════════════════════════════════════════════╗
║         ETH WALLET GENERATOR  v1.0                ║
║    High-Speed Ethereum Wallet Generator           ║
╚═══════════════════════════════════════════════════╝`)
}

func main() {
	count := flag.Int("n", 10, "number of wallets to generate")
	workers := flag.Int("workers", runtime.NumCPU(), "number of concurrent workers")
	output := flag.String("o", "", "output file (default: stdout)")
	showPriv := flag.Bool("priv", true, "show private key")
	showPub := flag.Bool("pub", false, "show public key")
	csvMode := flag.Bool("csv", false, "output as CSV format")
	flag.Parse()

	printBanner()

	boldYellow.Printf("\n[CONFIG] Generating %d wallets with %d workers\n\n", *count, *workers)

	var writer *bufio.Writer
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			boldRed.Printf("Error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		writer = bufio.NewWriterSize(f, 1<<20) // 1MB buffer
		defer writer.Flush()
		boldGreen.Printf("[OUTPUT] Saving to: %s\n\n", *output)
	}

	type result struct {
		idx int
		w   *wallet.Wallet
		err error
	}

	jobs := make(chan int, *count)
	results := make(chan result, *count)

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

	go func() {
		wg.Wait()
		close(results)
	}()

	for i := 1; i <= *count; i++ {
		jobs <- i
	}
	close(jobs)

	start := time.Now()
	var generated atomic.Int64
	var errCount atomic.Int64

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

	collected := make([]result, 0, *count)
	for r := range results {
		collected = append(collected, r)
	}

	// Sort by index for consistent output
	sorted := make([]result, *count)
	for _, r := range collected {
		if r.idx >= 1 && r.idx <= *count {
			sorted[r.idx-1] = r
		}
	}

	for _, r := range sorted {
		if r.err != nil {
			boldRed.Printf("[ERROR] #%d: %v\n", r.idx, r.err)
			errCount.Add(1)
			continue
		}
		generated.Add(1)

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
			fmt.Printf("Address:     ")
			boldCyan.Println(r.w.Address)

			if *showPriv {
				dimWhite.Printf("         PrivKey:     ")
				boldMagenta.Println("0x" + r.w.PrivateKey)
			}
			if *showPub {
				dimWhite.Printf("         PubKey:      ")
				fmt.Println("0x" + r.w.PublicKey[:32] + "...")
			}
			dimWhite.Println("         ─────────────────────────────────────────────")

			if writer != nil {
				line := fmt.Sprintf("[#%04d] Address: %s | PrivKey: 0x%s", r.idx, r.w.Address, r.w.PrivateKey)
				if *showPub {
					line += fmt.Sprintf(" | PubKey: 0x%s", r.w.PublicKey)
				}
				fmt.Fprintln(writer, line)
			}
		}
	}

	elapsed := time.Since(start)
	speed := float64(generated.Load()) / elapsed.Seconds()

	fmt.Println()
	boldGreen.Printf("✓ Generated:  %d wallets\n", generated.Load())
	if errCount.Load() > 0 {
		boldRed.Printf("✗ Errors:     %d\n", errCount.Load())
	}
	boldYellow.Printf("⚡ Speed:      %.0f wallets/sec\n", speed)
	boldCyan.Printf("⏱  Time:       %s\n", elapsed.Round(time.Millisecond))
	if *output != "" {
		boldGreen.Printf("💾 Saved to:   %s\n", *output)
	}
	fmt.Println()
}
