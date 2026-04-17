// Package rpc menyediakan RPC manager dengan batch call, dead endpoint detection,
// dan ERC-20 token balance checking.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── Endpoint ──────────────────────────────────────────────────────────────────

type Endpoint struct {
	URL       string
	mu        sync.Mutex
	fails     int
	deadUntil time.Time
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

func NewManager(urls []string, client *http.Client, timeout time.Duration, retries, deadThreshold int, deadCooldown time.Duration) *Manager {
	eps := make([]*Endpoint, len(urls))
	for i, u := range urls {
		eps[i] = &Endpoint{URL: u}
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

func (m *Manager) next() *Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := int64(len(m.endpoints))
	if n == 0 {
		return nil
	}
	// Coba endpoint yang hidup dulu
	for i := int64(0); i < n; i++ {
		ep := m.endpoints[(m.rpcIdx.Add(1)-1)%n]
		if ep.IsAlive() {
			return ep
		}
	}
	// Semua mati — kembalikan yang pertama tetap
	return m.endpoints[m.rpcIdx.Load()%n]
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
	Tokens map[string]*big.Int // token name → wei amount
}

// GetBalanceBatch mengirim banyak eth_getBalance dalam satu HTTP call.
// Juga menyertakan token ERC-20 jika tokens tidak kosong.
func (m *Manager) GetBalanceBatch(ctx context.Context, addresses []string, tokens []TokenCheck) (map[string]*AddressResult, error) {
	if len(addresses) == 0 {
		return map[string]*AddressResult{}, nil
	}

	// Bangun batch request
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
			// balanceOf(address) = 0x70a08231 + padded address
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

	// Inisialisasi hasil
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

		reqCtx, cancel := context.WithTimeout(ctx, m.timeout)
		err := m.doBatch(reqCtx, ep, body, idMap, results)
		cancel()

		if err != nil {
			ep.MarkFail(m.deadThreshold, m.deadCooldown)
			lastErr = err
			continue
		}

		ep.MarkSuccess()
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
