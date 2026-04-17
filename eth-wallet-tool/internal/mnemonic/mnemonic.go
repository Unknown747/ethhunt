// Package mnemonic menyediakan generate wallet Ethereum via BIP39/BIP44.
package mnemonic

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	bip32 "github.com/tyler-smith/go-bip32"
	bip39 "github.com/tyler-smith/go-bip39"

	"github.com/ethereum/go-ethereum/crypto"
)

// Wallet merepresentasikan wallet dari mnemonic BIP39
type Wallet struct {
	Mnemonic   string
	PrivateKey string
	PublicKey  string
	Address    string
	Path       string
	Index      uint32
}

// Generate membuat wallet baru dari mnemonic BIP39 acak
// words: 12 (128-bit entropy) atau 24 (256-bit entropy)
// index: indeks derivasi BIP44 (m/44'/60'/0'/0/index)
func Generate(words int, index uint32) (*Wallet, error) {
	bitSize := 128
	if words == 24 {
		bitSize = 256
	}

	entropy, err := bip39.NewEntropy(bitSize)
	if err != nil {
		return nil, fmt.Errorf("entropy: %w", err)
	}

	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, fmt.Errorf("mnemonic: %w", err)
	}

	return FromMnemonic(mnemonic, index)
}

// FromMnemonic menurunkan wallet dari mnemonic yang sudah ada
// Path derivasi: m/44'/60'/0'/0/index
func FromMnemonic(mnemonic string, index uint32) (*Wallet, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("mnemonic tidak valid")
	}

	seed := bip39.NewSeed(mnemonic, "")

	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}

	// m/44'/60'/0'/0/index
	path := []uint32{
		bip32.FirstHardenedChild + 44, // purpose
		bip32.FirstHardenedChild + 60, // coin type (Ethereum)
		bip32.FirstHardenedChild + 0,  // account
		0,                             // change (external)
		index,                         // address index
	}

	child := masterKey
	for _, idx := range path {
		child, err = child.NewChildKey(idx)
		if err != nil {
			return nil, fmt.Errorf("derive key: %w", err)
		}
	}

	privKey, err := crypto.ToECDSA(child.Key)
	if err != nil {
		return nil, fmt.Errorf("ecdsa: %w", err)
	}

	pubKey := privKey.Public().(*ecdsa.PublicKey)
	address := crypto.PubkeyToAddress(*pubKey)

	return &Wallet{
		Mnemonic:   mnemonic,
		PrivateKey: hex.EncodeToString(crypto.FromECDSA(privKey)),
		PublicKey:  hex.EncodeToString(crypto.FromECDSAPub(pubKey)),
		Address:    address.Hex(),
		Path:       fmt.Sprintf("m/44'/60'/0'/0/%d", index),
		Index:      index,
	}, nil
}
