// Package notify mengirim notifikasi ke Telegram secara non-blocking.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type TelegramConfig struct {
	Enabled bool
	Token   string
	ChatID  string
}

type Telegram struct {
	cfg    TelegramConfig
	client *http.Client
	queue  chan string
}

func NewTelegram(cfg TelegramConfig) *Telegram {
	return &Telegram{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		queue: make(chan string, 200),
	}
}

// Start memulai goroutine pengirim pesan (non-blocking)
func (t *Telegram) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-t.queue:
				if !ok {
					return
				}
				if t.cfg.Enabled && t.cfg.Token != "" && t.cfg.ChatID != "" {
					t.send(msg)
				}
			}
		}
	}()
}

// Notify menambahkan pesan ke antrian (tidak memblokir)
func (t *Telegram) Notify(msg string) {
	select {
	case t.queue <- msg:
	default: // buang jika antrian penuh
	}
}

func (t *Telegram) send(text string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.cfg.Token)

	payload := map[string]string{
		"chat_id":    t.cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	body, _ := json.Marshal(payload)

	resp, err := t.client.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil || resp == nil {
		return
	}
	resp.Body.Close()
}

// SendTest mengirim pesan test dan mengembalikan error jika gagal
func (t *Telegram) SendTest() error {
	if !t.cfg.Enabled {
		return fmt.Errorf("telegram tidak diaktifkan di config.yaml")
	}
	if t.cfg.Token == "" || t.cfg.ChatID == "" {
		return fmt.Errorf("token atau chat_id belum diisi di config.yaml")
	}

	msg := "🤖 <b>ETH Wallet Tool</b>\n✅ Koneksi Telegram berhasil!\nNotifikasi akan dikirim saat wallet dengan balance ditemukan."
	t.send(msg)
	return nil
}

// FormatFound memformat pesan temuan wallet untuk Telegram
func FormatFound(address, privKey string, ethBal float64, currency string, tokens map[string]float64) string {
	msg := fmt.Sprintf("🚨 <b>WALLET DITEMUKAN!</b>\n\n")
	msg += fmt.Sprintf("📌 <b>Address:</b>\n<code>%s</code>\n\n", address)
	msg += fmt.Sprintf("🔑 <b>Private Key:</b>\n<code>0x%s</code>\n\n", privKey)
	msg += fmt.Sprintf("💰 <b>Balance %s:</b> %.8f\n", currency, ethBal)
	for name, bal := range tokens {
		if bal > 0 {
			msg += fmt.Sprintf("🪙 <b>%s:</b> %.6f\n", name, bal)
		}
	}
	return msg
}
