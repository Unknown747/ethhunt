package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProxySource adalah URL sumber daftar proxy gratis
var ProxySources = []string{
	"https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt",
	"https://raw.githubusercontent.com/clarketm/proxy-list/master/proxy-list-raw.txt",
	"https://raw.githubusercontent.com/ShiftyTR/Proxy-List/master/http.txt",
	"https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/http.txt",
	"https://raw.githubusercontent.com/jetkai/proxy-list/main/online-proxies/txt/proxies-http.txt",
	"https://raw.githubusercontent.com/mertguvencli/http-proxy-list/main/proxy-list/data.txt",
	"https://raw.githubusercontent.com/roosterkid/openproxylist/main/HTTPS_RAW.txt",
}

// Proxy merepresentasikan satu proxy HTTP
type Proxy struct {
	Address string // host:port
	Fails   int
}

func (p *Proxy) URL() string {
	if strings.HasPrefix(p.Address, "http") {
		return p.Address
	}
	return "http://" + p.Address
}

// Manager mengelola pool proxy dengan auto-fetch dan health check
type Manager struct {
	mu          sync.RWMutex
	proxies     []*Proxy
	idx         atomic.Int64
	filePath    string
	fetching    atomic.Bool
	maxFails    int
	fetchClient *http.Client
	OnRefetch   func(count int) // callback saat refetch
	OnRemove    func(addr string) // callback saat proxy dibuang
}

// NewManager membuat proxy manager baru
func NewManager(filePath string, maxFails int) *Manager {
	return &Manager{
		filePath: filePath,
		maxFails: maxFails,
		fetchClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    50,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

// Load membaca proxies dari file
func (m *Manager) Load() error {
	f, err := os.Open(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	var loaded []*Proxy
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		loaded = append(loaded, &Proxy{Address: line})
	}

	m.mu.Lock()
	m.proxies = loaded
	m.mu.Unlock()
	return scanner.Err()
}

// Count returns jumlah proxy aktif
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.proxies)
}

// Next mengambil proxy berikutnya (round-robin)
func (m *Manager) Next() *Proxy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.proxies) == 0 {
		return nil
	}
	idx := m.idx.Add(1) - 1
	return m.proxies[idx%int64(len(m.proxies))]
}

// Random mengambil proxy secara acak
func (m *Manager) Random() *Proxy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.proxies) == 0 {
		return nil
	}
	return m.proxies[rand.Intn(len(m.proxies))]
}

// MarkFailed menandai proxy gagal, hapus jika melebihi batas
func (m *Manager) MarkFailed(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.proxies {
		if p.Address == addr {
			p.Fails++
			if p.Fails >= m.maxFails {
				m.proxies = append(m.proxies[:i], m.proxies[i+1:]...)
				if m.OnRemove != nil {
					go m.OnRemove(addr)
				}
			}
			break
		}
	}

	// Auto-refetch jika tinggal sedikit atau habis
	if len(m.proxies) == 0 {
		go m.FetchAndValidate(context.Background(), 0)
	}
}

// Save menyimpan proxy aktif ke file
func (m *Manager) Save() error {
	m.mu.RLock()
	proxies := make([]*Proxy, len(m.proxies))
	copy(proxies, m.proxies)
	m.mu.RUnlock()

	f, err := os.Create(m.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# ETH Wallet Tool Proxies - Updated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(w, "# Total: %d proxies\n\n", len(proxies))
	for _, p := range proxies {
		fmt.Fprintln(w, p.Address)
	}
	return w.Flush()
}

// FetchRaw mengambil daftar proxy mentah dari satu sumber
func (m *Manager) FetchRaw(ctx context.Context, sourceURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ETH-Tool/1.0)")

	resp, err := m.fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, sourceURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // max 10MB
	if err != nil {
		return nil, err
	}

	var result []string
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Normalisasi format
		line = strings.TrimPrefix(line, "http://")
		line = strings.TrimPrefix(line, "https://")
		// Filter: hanya ambil yang formatnya host:port
		if strings.Contains(line, ":") && !strings.Contains(line, "/") {
			result = append(result, line)
		}
	}
	return result, nil
}

// TestProxy menguji apakah proxy berfungsi dengan timeout tertentu
func TestProxy(proxyAddr string, timeout time.Duration) bool {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: timeout / 2,
		},
	}

	// Test dengan call ke Ethereum RPC
	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	resp, err := client.Post("https://eth.llamarpc.com", "application/json", strings.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// FetchAndValidate mengambil, validasi, dan simpan proxy baru
// workers: jumlah goroutine validator (0 = default 50)
func (m *Manager) FetchAndValidate(ctx context.Context, workers int) {
	if !m.fetching.CompareAndSwap(false, true) {
		return // Sudah ada proses fetch
	}
	defer m.fetching.Store(false)

	if workers <= 0 {
		workers = 50
	}

	// Kumpulkan semua proxy dari semua sumber
	rawSet := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, src := range ProxySources {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			proxies, err := m.FetchRaw(ctx, source)
			if err != nil {
				return
			}
			mu.Lock()
			for _, p := range proxies {
				rawSet[p] = true
			}
			mu.Unlock()
		}(src)
	}
	wg.Wait()

	// Deduplicate + filter yang sudah ada
	m.mu.RLock()
	existing := make(map[string]bool, len(m.proxies))
	for _, p := range m.proxies {
		existing[p.Address] = true
	}
	m.mu.RUnlock()

	var candidates []string
	for addr := range rawSet {
		if !existing[addr] {
			candidates = append(candidates, addr)
		}
	}

	if len(candidates) == 0 {
		return
	}

	// Validasi concurrent
	jobs := make(chan string, len(candidates))
	valid := make(chan string, len(candidates))

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if TestProxy(addr, 8*time.Second) {
					valid <- addr
				}
			}
		}()
	}

	for _, c := range candidates {
		jobs <- c
	}
	close(jobs)

	wg.Wait()
	close(valid)

	// Tambahkan proxy valid ke pool
	var newProxies []*Proxy
	for addr := range valid {
		newProxies = append(newProxies, &Proxy{Address: addr})
	}

	if len(newProxies) > 0 {
		m.mu.Lock()
		m.proxies = append(m.proxies, newProxies...)
		m.mu.Unlock()

		m.Save()

		if m.OnRefetch != nil {
			m.OnRefetch(len(newProxies))
		}
	}
}

// AutoRefresh memantau proxy pool dan refetch otomatis jika hampir habis
func (m *Manager) AutoRefresh(ctx context.Context, minThreshold int, checkInterval time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.Count() < minThreshold && !m.fetching.Load() {
				go m.FetchAndValidate(ctx, 50)
			}
		}
	}
}

// BuildHTTPClient membuat http.Client yang menggunakan proxy
func BuildHTTPClient(proxyAddr string, timeout time.Duration) *http.Client {
	if proxyAddr == "" {
		return &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     60 * time.Second,
			},
		}
	}

	proxyURL, _ := url.Parse("http://" + proxyAddr)
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: timeout / 2,
		},
	}
}
