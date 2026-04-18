// Package rpc menyediakan RPC manager dengan batch call, dead endpoint detection,
// latency-priority routing, rate limiting, dan ERC-20 token balance checking.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── Token Bucket Rate Limiter ─────────────────────────────────────────────────

type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	maxTok   float64
	ratePerS float64
	lastTime time.Time
}

func newTokenBucket(ratePerSec int) *tokenBucket {
	if ratePerSec <= 0 {
		return nil
	}
	r := float64(ratePerSec)
	return &tokenBucket{
		tokens:   r,
		maxTok:   r,
		ratePerS: r,
		lastTime: time.Now(),
	}
}

func (tb *tokenBucket) Wait(ctx context.Context) error {
	if tb == nil {
		return nil
	}
	for {
		tb.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(tb.lastTime).Seconds()
		tb.lastTime = now
		tb.tokens += elapsed * tb.ratePerS
		if tb.tokens > tb.maxTok {
			tb.tokens = tb.maxTok
		}
		if tb.tokens >= 1 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		waitDur := time.Duration((1-tb.tokens)/tb.ratePerS*float64(time.Second)) + time.Millisecond
		tb.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
}

// ── EndpointSpec ──────────────────────────────────────────────────────────────

// EndpointSpec mendefinisikan satu RPC endpoint dengan URL dan optional custom headers.
// Berguna untuk endpoint berbayar (Infura, Alchemy, QuickNode) yang butuh auth header.
type EndpointSpec struct {
	URL     string
	Headers map[string]string // opsional: custom HTTP headers
}

// ── Endpoint ──────────────────────────────────────────────────────────────────

type Endpoint struct {
	URL        string
	headers    map[string]string
	mu         sync.Mutex
	fails      int
	deadUntil  time.Time
	avgLatency time.Duration // EWMA latency — dipakai untuk prioritas routing
	limiter    *tokenBucket
}

func (e *Endpoint) IsAlive() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deadUntil.IsZero() || time.Now().After(e.deadUntil)
}

func (e *Endpoint) MarkFail(threshold int, cooldown time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fails++
	if e.fails >= threshold {
		e.deadUntil = time.Now().Add(cooldown)
		e.fails = 0
	}
}

func (e *Endpoint) MarkSuccess() {
	e.mu.Lock()
	e.fails = 0
	e.deadUntil = time.Time{}
	e.mu.Unlock()
}

// updateLatency memperbarui rata-rata latency dengan EWMA (α=0.3)
func (e *Endpoint) updateLatency(sample time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.avgLatency == 0 {
		e.avgLatency = sample
	} else {
		const alpha = 0.3
		e.avgLatency = time.Duration(float64(e.avgLatency)*(1-alpha) + float64(sample)*alpha)
	}
}

// AvgLatency mengembalikan rata-rata latency endpoint
func (e *Endpoint) AvgLatency() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.avgLatency
}

// ── Manager ───────────────────────────────────────────────────────────────────

type Manager struct {
	endpoints     []*Endpoint
	rpcIdx        atomic.Int64
	mu            sync.RWMutex
	client        *http.Client
	timeout       time.Duration
	retries       int
	deadThreshold int
	deadCooldown  time.Duration
}

// NewManager membuat RPC manager baru dari daftar EndpointSpec.
// ratePerSec: maksimal request per detik per endpoint (0 = unlimited).
func NewManager(specs []EndpointSpec, client *http.Client, timeout time.Duration, retries, deadThreshold int, deadCooldown time.Duration, ratePerSec int) *Manager {
	eps := make([]*Endpoint, len(specs))
	for i, s := range specs {
		eps[i] = &Endpoint{
			URL:     s.URL,
			headers: s.Headers,
			limiter: newTokenBucket(ratePerSec),
		}
	}
	return &Manager{
		endpoints:     eps,
		client:        client,
		timeout:       timeout,
		retries:       retries,
		deadThreshold: deadThreshold,
		deadCooldown:  deadCooldown,
	}
}

// next memilih endpoint berikutnya berdasarkan latency (tercepat diprioritaskan).
// Jitter ditambahkan agar beban tersebar dan tidak hammering satu endpoint.
func (m *Manager) next() *Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.endpoints) == 0 {
		return nil
	}

	var best *Endpoint
	var bestScore float64

	for _, ep := range m.endpoints {
		if !ep.IsAlive() {
			continue
		}
		lat := ep.AvgLatency().Milliseconds()
		if lat <= 0 {
			lat = 1
		}
		// Jitter 0.7–1.3 → distribusi beban, tetap memprioritaskan endpoint cepat
		jitter := 0.7 + rand.Float64()*0.6
		score := float64(lat) * jitter
		if best == nil || score < bestScore {
			best = ep
			bestScore = score
		}
	}

	if best != nil {
		return best
	}

	// Semua mati → fallback round-robin biasa
	n := int64(len(m.endpoints))
	return m.endpoints[(m.rpcIdx.Add(1)-1)%n]
}

// AliveCount mengembalikan jumlah endpoint yang aktif
func (m *Manager) AliveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, ep := range m.endpoints {
		if ep.IsAlive() {
			count++
		}
	}
	return count
}

// TotalCount mengembalikan total jumlah endpoint
func (m *Manager) TotalCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.endpoints)
}

// EndpointLatencies mengembalikan URL dan latency setiap endpoint (untuk display)
func (m *Manager) EndpointLatencies() []struct {
	URL     string
	Latency time.Duration
	Alive   bool
} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]struct {
		URL     string
		Latency time.Duration
		Alive   bool
	}, len(m.endpoints))
	for i, ep := range m.endpoints {
		out[i] = struct {
			URL     string
			Latency time.Duration
			Alive   bool
		}{ep.URL, ep.AvgLatency(), ep.IsAlive()}
	}
	return out
}

// ── JSON-RPC types ────────────────────────────────────────────────────────────

type rpcReq struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type rpcResp struct {
	ID     int         `json:"id"`
	Result interface{} `json:"result"`
	Error  *rpcErr     `json:"error"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── Batch Balance ──────────────────────────────────────────────────────────────

// TokenCheck mendefinisikan token ERC-20 yang akan dicek
type TokenCheck struct {
	Name     string
	Address  string
	Decimals int
}

// AddressResult menyimpan semua hasil untuk satu wallet address
type AddressResult struct {
	ETH    *big.Int
	Tokens map[string]*big.Int
}

// GetBalanceBatch mengirim banyak eth_getBalance dalam satu HTTP call.
func (m *Manager) GetBalanceBatch(ctx context.Context, addresses []string, tokens []TokenCheck) (map[string]*AddressResult, error) {
	if len(addresses) == 0 {
		return map[string]*AddressResult{}, nil
	}

	reqs := make([]rpcReq, 0, len(addresses)*(1+len(tokens)))
	id := 1
	idMap := make(map[int]struct{ addr, typ, tokenName string })

	for _, addr := range addresses {
		idMap[id] = struct{ addr, typ, tokenName string }{addr, "eth", ""}
		reqs = append(reqs, rpcReq{
			JSONRPC: "2.0", Method: "eth_getBalance",
			Params: []interface{}{addr, "latest"}, ID: id,
		})
		id++

		for _, tok := range tokens {
			data := "0x70a08231" + fmt.Sprintf("%064s", strings.TrimPrefix(addr, "0x"))
			idMap[id] = struct{ addr, typ, tokenName string }{addr, "token", tok.Name}
			reqs = append(reqs, rpcReq{
				JSONRPC: "2.0", Method: "eth_call",
				Params: []interface{}{map[string]string{"to": tok.Address, "data": data}, "latest"},
				ID:     id,
			})
			id++
		}
	}

	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}

	results := make(map[string]*AddressResult, len(addresses))
	for _, addr := range addresses {
		results[addr] = &AddressResult{
			ETH:    big.NewInt(0),
			Tokens: make(map[string]*big.Int),
		}
	}

	var lastErr error
	for attempt := 0; attempt <= m.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			}
		}

		ep := m.next()
		if ep == nil {
			return nil, fmt.Errorf("tidak ada RPC endpoint")
		}

		if err := ep.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		reqCtx, cancel := context.WithTimeout(ctx, m.timeout)
		start := time.Now()
		err := m.doBatch(reqCtx, ep, body, idMap, results)
		latency := time.Since(start)
		cancel()

		if err != nil {
			ep.MarkFail(m.deadThreshold, m.deadCooldown)
			lastErr = err
			continue
		}

		ep.MarkSuccess()
		ep.updateLatency(latency)
		return results, nil
	}

	return nil, lastErr
}

func (m *Manager) doBatch(
	ctx context.Context,
	ep *Endpoint,
	body []byte,
	idMap map[int]struct{ addr, typ, tokenName string },
	out map[string]*AddressResult,
) error {
	req, err := http.NewRequestWithContext(ctx, "POST", ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range ep.headers {
		req.Header.Set(k, v)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}

	var responses []rpcResp
	if err := json.Unmarshal(data, &responses); err != nil {
		return fmt.Errorf("batch unmarshal: %w", err)
	}

	for _, r := range responses {
		info, ok := idMap[r.ID]
		if !ok {
			continue
		}
		res, ok := out[info.addr]
		if !ok {
			continue
		}

		if r.Error != nil {
			continue
		}

		hexStr, ok := r.Result.(string)
		if !ok {
			continue
		}
		hexStr = strings.TrimPrefix(hexStr, "0x")
		if hexStr == "" {
			hexStr = "0"
		}
		n := new(big.Int)
		n.SetString(hexStr, 16)

		if info.typ == "eth" {
			res.ETH = n
		} else {
			res.Tokens[info.tokenName] = n
		}
	}

	return nil
}

// GetBalance mengecek balance satu address (wrapper dari batch)
func (m *Manager) GetBalance(ctx context.Context, address string, tokens []TokenCheck) (*AddressResult, error) {
	results, err := m.GetBalanceBatch(ctx, []string{address}, tokens)
	if err != nil {
		return nil, err
	}
	r, ok := results[address]
	if !ok {
		return &AddressResult{ETH: big.NewInt(0), Tokens: map[string]*big.Int{}}, nil
	}
	return r, nil
}

// ── Health Check ──────────────────────────────────────────────────────────────

// EndpointStatus hasil health check satu endpoint
type EndpointStatus struct {
	URL     string
	Alive   bool
	Latency time.Duration
}

// HealthCheck menguji semua endpoint secara concurrent dan menyimpan latency.
func (m *Manager) HealthCheck(timeout time.Duration) []EndpointStatus {
	m.mu.RLock()
	eps := make([]*Endpoint, len(m.endpoints))
	copy(eps, m.endpoints)
	m.mu.RUnlock()

	results := make([]EndpointStatus, len(eps))
	var wg sync.WaitGroup

	for i, ep := range eps {
		wg.Add(1)
		go func(idx int, e *Endpoint) {
			defer wg.Done()
			start := time.Now()
			alive := m.pingEndpoint(e.URL, e.headers, timeout)
			latency := time.Since(start)
			results[idx] = EndpointStatus{URL: e.URL, Alive: alive, Latency: latency}

			e.mu.Lock()
			if alive {
				// Simpan latency awal dari health check
				if e.avgLatency == 0 {
					e.avgLatency = latency
				}
			} else {
				e.deadUntil = time.Now().Add(m.deadCooldown)
			}
			e.mu.Unlock()
		}(i, ep)
	}

	wg.Wait()
	return results
}

// ReCheckDead menguji ulang hanya endpoint yang saat ini mati.
// Jika endpoint merespons kembali, status-nya di-reset (revived).
// Mengembalikan jumlah yang berhasil dihidupkan kembali dan total yang diperiksa.
func (m *Manager) ReCheckDead(timeout time.Duration) (revived, total int) {
	m.mu.RLock()
	eps := make([]*Endpoint, len(m.endpoints))
	copy(eps, m.endpoints)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ep := range eps {
		if ep.IsAlive() {
			continue
		}
		total++
		wg.Add(1)
		go func(e *Endpoint) {
			defer wg.Done()
			start := time.Now()
			if m.pingEndpoint(e.URL, e.headers, timeout) {
				latency := time.Since(start)
				e.mu.Lock()
				e.deadUntil = time.Time{}
				e.fails = 0
				if e.avgLatency == 0 {
					e.avgLatency = latency
				}
				e.mu.Unlock()
				mu.Lock()
				revived++
				mu.Unlock()
			}
		}(ep)
	}

	wg.Wait()
	return
}

// pingEndpoint mengirim eth_blockNumber ke satu endpoint dan cek responsnya
func (m *Manager) pingEndpoint(url string, headers map[string]string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	body := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var result struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Result != ""
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// WeiToDecimal mengkonversi wei ke unit desimal (ETH, USDT, dll)
func WeiToDecimal(wei *big.Int, decimals int) *big.Float {
	if wei == nil {
		return new(big.Float)
	}
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Float).Quo(new(big.Float).SetInt(wei), new(big.Float).SetInt(d))
}

// HasAnyBalance mengembalikan true jika address punya ETH atau token > 0
func HasAnyBalance(r *AddressResult) bool {
	if r.ETH != nil && r.ETH.Cmp(big.NewInt(0)) > 0 {
		return true
	}
	for _, bal := range r.Tokens {
		if bal != nil && bal.Cmp(big.NewInt(0)) > 0 {
			return true
		}
	}
	return false
}
