package config

import (
        "fmt"
        "os"
        "time"

        "gopkg.in/yaml.v3"
)

type Config struct {
        Workers   WorkersConfig          `yaml:"workers"`
        RPC       RPCConfig              `yaml:"rpc"`
        Proxy     ProxyConfig            `yaml:"proxy"`
        Chain     string                 `yaml:"chain"`
        Chains    map[string]ChainConfig `yaml:"chains"`
        Tokens    TokensConfig           `yaml:"tokens"`
        Telegram  TelegramConfig         `yaml:"telegram"`
        Generator GeneratorConfig        `yaml:"generator"`
        Output    OutputConfig           `yaml:"output"`
        Hunt      HuntConfig             `yaml:"hunt"`
}

type WorkersConfig struct {
        Hunt      int `yaml:"hunt"`
        Checker   int `yaml:"checker"`
        Generator int `yaml:"generator"`
}

type RPCConfig struct {
        BatchSize     int           `yaml:"batch_size"`
        Timeout       time.Duration `yaml:"timeout"`
        Retries       int           `yaml:"retries"`
        DeadThreshold int           `yaml:"dead_threshold"`
        DeadCooldown  time.Duration `yaml:"dead_cooldown"`
        RateLimit     int           `yaml:"rate_limit"` // maks request/detik per endpoint (0 = unlimited)
}

type ProxyConfig struct {
        Enabled         bool   `yaml:"enabled"`
        File            string `yaml:"file"`
        MaxFails        int    `yaml:"max_fails"`
        AutoFetch       bool   `yaml:"auto_fetch"`
        ValidateWorkers int    `yaml:"validate_workers"`
}

type ChainConfig struct {
        Name     string   `yaml:"name"`
        Currency string   `yaml:"currency"`
        ChainID  int64    `yaml:"chain_id"`
        RPC      []string `yaml:"rpc"`
}

type TokenConfig struct {
        Name     string `yaml:"name"`
        Address  string `yaml:"address"`
        Decimals int    `yaml:"decimals"`
}

type TokensConfig struct {
        CheckERC20 bool          `yaml:"check_erc20"`
        List       []TokenConfig `yaml:"list"`
}

type TelegramConfig struct {
        Enabled bool   `yaml:"enabled"`
        Token   string `yaml:"token"`
        ChatID  string `yaml:"chat_id"`
}

type GeneratorConfig struct {
        Mode          string `yaml:"mode"`
        MnemonicWords int    `yaml:"mnemonic_words"`
}

type OutputConfig struct {
        FoundFile  string `yaml:"found_file"`
        StatsLog   string `yaml:"stats_log"`
        ResumeFile string `yaml:"resume_file"`
}

type HuntConfig struct {
        StatsInterval time.Duration `yaml:"stats_interval"`
}

// Load membaca config.yaml; jika tidak ada, kembalikan default
func Load(path string) (*Config, error) {
        cfg := Default()

        f, err := os.Open(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return cfg, nil
                }
                return nil, fmt.Errorf("buka config: %w", err)
        }
        defer f.Close()

        if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
                return nil, fmt.Errorf("parse config: %w", err)
        }

        cfg.applyDefaults()
        return cfg, nil
}

// Save menyimpan config ke file
func Save(cfg *Config, path string) error {
        f, err := os.Create(path)
        if err != nil {
                return err
        }
        defer f.Close()
        enc := yaml.NewEncoder(f)
        enc.SetIndent(2)
        return enc.Encode(cfg)
}

// Default mengembalikan konfigurasi default
func Default() *Config {
        return &Config{
                Workers: WorkersConfig{},
                RPC: RPCConfig{
                        BatchSize:     20,
                        Timeout:       12 * time.Second,
                        Retries:       3,
                        DeadThreshold: 5,
                        DeadCooldown:  5 * time.Minute,
                },
                Proxy: ProxyConfig{
                        File:            "proxies.txt",
                        MaxFails:        3,
                        ValidateWorkers: 50,
                },
                Chain: "ethereum",
                Chains: map[string]ChainConfig{
                        "ethereum": {
                                Name: "Ethereum Mainnet", Currency: "ETH", ChainID: 1,
                                RPC: []string{
                                        "https://eth.llamarpc.com",
                                        "https://ethereum.publicnode.com",
                                        "https://eth-mainnet.public.blastapi.io",
                                        "https://rpc.payload.de",
                                        "https://1rpc.io/eth",
                                },
                        },
                        "bsc": {
                                Name: "BNB Smart Chain", Currency: "BNB", ChainID: 56,
                                RPC: []string{
                                        "https://bsc-dataseed1.binance.org",
                                        "https://bsc-dataseed2.binance.org",
                                        "https://bsc-dataseed3.binance.org",
                                },
                        },
                        "polygon": {
                                Name: "Polygon", Currency: "MATIC", ChainID: 137,
                                RPC: []string{
                                        "https://polygon-rpc.com",
                                        "https://rpc-mainnet.maticvigil.com",
                                },
                        },
                        "arbitrum": {
                                Name: "Arbitrum One", Currency: "ETH", ChainID: 42161,
                                RPC: []string{
                                        "https://arb1.arbitrum.io/rpc",
                                        "https://rpc.ankr.com/arbitrum",
                                },
                        },
                },
                Tokens: TokensConfig{
                        CheckERC20: false,
                        List: []TokenConfig{
                                {Name: "USDT", Address: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6},
                                {Name: "USDC", Address: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Decimals: 6},
                        },
                },
                Telegram: TelegramConfig{Enabled: false},
                Generator: GeneratorConfig{
                        Mode:          "random",
                        MnemonicWords: 12,
                },
                Output: OutputConfig{
                        FoundFile:  "found.txt",
                        StatsLog:   "stats.csv",
                        ResumeFile: ".hunt_resume",
                },
                Hunt: HuntConfig{StatsInterval: 5 * time.Second},
        }
}

func (c *Config) applyDefaults() {
        if c.RPC.BatchSize <= 0 {
                c.RPC.BatchSize = 20
        }
        if c.RPC.Timeout == 0 {
                c.RPC.Timeout = 12 * time.Second
        }
        if c.RPC.Retries <= 0 {
                c.RPC.Retries = 3
        }
        if c.RPC.DeadThreshold <= 0 {
                c.RPC.DeadThreshold = 5
        }
        if c.RPC.DeadCooldown == 0 {
                c.RPC.DeadCooldown = 5 * time.Minute
        }
        if c.Chain == "" {
                c.Chain = "ethereum"
        }
        if c.Proxy.File == "" {
                c.Proxy.File = "proxies.txt"
        }
        if c.Proxy.MaxFails <= 0 {
                c.Proxy.MaxFails = 3
        }
        if c.Proxy.ValidateWorkers <= 0 {
                c.Proxy.ValidateWorkers = 50
        }
        if c.Generator.Mode == "" {
                c.Generator.Mode = "random"
        }
        if c.Generator.MnemonicWords == 0 {
                c.Generator.MnemonicWords = 12
        }
        if c.Output.FoundFile == "" {
                c.Output.FoundFile = "found.txt"
        }
        if c.Output.ResumeFile == "" {
                c.Output.ResumeFile = ".hunt_resume"
        }
        if c.Hunt.StatsInterval == 0 {
                c.Hunt.StatsInterval = 5 * time.Second
        }
        // Isi chains default jika kosong
        if len(c.Chains) == 0 {
                c.Chains = Default().Chains
        }
        // Isi tokens default jika kosong
        if len(c.Tokens.List) == 0 {
                c.Tokens.List = Default().Tokens.List
        }
}

// GetChainRPCs mengembalikan daftar RPC untuk chain tertentu
func (c *Config) GetChainRPCs(chainName string) ([]string, string, error) {
        chain, ok := c.Chains[chainName]
        if !ok {
                return nil, "", fmt.Errorf("chain '%s' tidak ditemukan di config", chainName)
        }
        if len(chain.RPC) == 0 {
                return nil, "", fmt.Errorf("chain '%s' tidak punya RPC endpoint", chainName)
        }
        return chain.RPC, chain.Currency, nil
}
