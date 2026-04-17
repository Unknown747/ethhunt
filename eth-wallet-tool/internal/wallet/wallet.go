package wallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Wallet represents an Ethereum wallet
type Wallet struct {
	PrivateKey string
	PublicKey  string
	Address    string
}

// Generate creates a new random Ethereum wallet
func Generate() (*Wallet, error) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return FromPrivateKey(privateKey), nil
}

// FromPrivateKey derives wallet data from an ECDSA private key
func FromPrivateKey(privateKey *ecdsa.PrivateKey) *Wallet {
	privateKeyBytes := crypto.FromECDSA(privateKey)
	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	publicKeyBytes := crypto.FromECDSAPub(publicKey)
	address := crypto.PubkeyToAddress(*publicKey)

	return &Wallet{
		PrivateKey: hex.EncodeToString(privateKeyBytes),
		PublicKey:  hex.EncodeToString(publicKeyBytes),
		Address:    address.Hex(),
	}
}

// FromPrivateKeyHex derives wallet from hex private key string
func FromPrivateKeyHex(privKeyHex string) (*Wallet, error) {
	privBytes, err := hex.DecodeString(stripHexPrefix(privKeyHex))
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	privateKey, err := crypto.ToECDSA(privBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return FromPrivateKey(privateKey), nil
}

// IsValidAddress checks if a string is a valid Ethereum address
func IsValidAddress(address string) bool {
	return common.IsHexAddress(address)
}

func stripHexPrefix(s string) string {
	if len(s) > 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}
