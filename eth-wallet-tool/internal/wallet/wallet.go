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
	return fromPrivateKey(privateKey), nil
}

// fromPrivateKey derives wallet data from an ECDSA private key
func fromPrivateKey(privateKey *ecdsa.PrivateKey) *Wallet {
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

// IsValidAddress checks if a string is a valid Ethereum address
func IsValidAddress(address string) bool {
	return common.IsHexAddress(address)
}
