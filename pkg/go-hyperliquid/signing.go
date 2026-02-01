package hyperliquid

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/vmihailenco/msgpack/v5"
)

// addressToBytes converts a hex address to bytes, matching Python's address_to_bytes
func addressToBytes(address string) []byte {
	address = strings.TrimPrefix(address, "0x")
	bytes, _ := hex.DecodeString(address)
	return bytes
}

// actionHash implements the same logic as Python's action_hash function
func actionHash(action any, vaultAddress string, nonce int64, expiresAfter *int64) []byte {
	// Pack action using msgpack (like Python's msgpack.packb)
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	enc.SetSortMapKeys(true)
	enc.UseCompactInts(true)

	err := enc.Encode(action)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal action: %v", err))
	}
	data := buf.Bytes()

	// Add nonce as 8 bytes big endian
	if nonce < 0 {
		panic(fmt.Sprintf("nonce cannot be negative: %d", nonce))
	}
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, uint64(nonce))
	data = append(data, nonceBytes...)

	// Add vault address
	if vaultAddress == "" {
		data = append(data, 0x00)
	} else {
		data = append(data, 0x01)
		data = append(data, addressToBytes(vaultAddress)...)
	}

	// Add expires_after if provided
	if expiresAfter != nil {
		if *expiresAfter < 0 {
			panic(fmt.Sprintf("expiresAfter cannot be negative: %d", *expiresAfter))
		}
		data = append(data, 0x00)
		expiresAfterBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(expiresAfterBytes, uint64(*expiresAfter))
		data = append(data, expiresAfterBytes...)
	}

	// Return keccak256 hash
	hash := crypto.Keccak256(data)
	// fmt.Printf("go action hash: %s\n", hex.EncodeToString(hash))
	return hash
}

// constructPhantomAgent implements the same logic as Python's construct_phantom_agent
func constructPhantomAgent(hash []byte, isMainnet bool) map[string]any {
	source := "b" // testnet
	if isMainnet {
		source = "a" // mainnet
	}
	return map[string]any{
		"source":       source,
		"connectionId": hash,
	}
}

// l1Payload implements the same logic as Python's l1_payload
func l1Payload(phantomAgent map[string]any) apitypes.TypedData {
	chainId := math.HexOrDecimal256(*big.NewInt(1337))
	return apitypes.TypedData{
		Domain: apitypes.TypedDataDomain{
			ChainId:           &chainId,
			Name:              "Exchange",
			Version:           "1",
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: apitypes.Types{
			"Agent": []apitypes.Type{
				{Name: "source", Type: "string"},
				{Name: "connectionId", Type: "bytes32"},
			},
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		PrimaryType: "Agent",
		Message:     phantomAgent,
	}
}

// SignatureResult represents the structured signature result
type SignatureResult struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

// signInner implements the same logic as Python's sign_inner
func signInner(
	privateKey *ecdsa.PrivateKey,
	typedData apitypes.TypedData,
) (SignatureResult, error) {
	// Create EIP-712 hash
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return SignatureResult{}, fmt.Errorf("failed to hash domain: %w", err)
	}

	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return SignatureResult{}, fmt.Errorf("failed to hash typed data: %w", err)
	}

	rawData := []byte{0x19, 0x01}
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, typedDataHash...)
	msgHash := crypto.Keccak256Hash(rawData)

	signature, err := crypto.Sign(msgHash.Bytes(), privateKey)
	if err != nil {
		return SignatureResult{}, fmt.Errorf("failed to sign message: %w", err)
	}

	// Extract r, s, v components
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:64])
	v := int(signature[64]) + 27

	return SignatureResult{
		R: hexutil.EncodeBig(r),
		S: hexutil.EncodeBig(s),
		V: v,
	}, nil
}

// SignL1Action implements the same logic as Python's sign_l1_action
func SignL1Action(
	privateKey *ecdsa.PrivateKey,
	action any,
	vaultAddress string,
	timestamp int64,
	expiresAfter *int64,
	isMainnet bool,
) (SignatureResult, error) {
	// Step 1: Create action hash
	hash := actionHash(action, vaultAddress, timestamp, expiresAfter)

	// Step 2: Construct phantom agent
	phantomAgent := constructPhantomAgent(hash, isMainnet)

	// Step 3: Create l1 payload
	typedData := l1Payload(phantomAgent)

	// Step 4: Sign using EIP-712
	return signInner(privateKey, typedData)
}

// SignUsdClassTransferAction signs USD class transfer action
func SignUsdClassTransferAction(
	privateKey *ecdsa.PrivateKey,
	amount float64,
	toPerp bool,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":   "usdClassTransfer",
		"amount": amount,
		"toPerp": toPerp,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignSpotTransferAction signs spot transfer action
func SignSpotTransferAction(
	privateKey *ecdsa.PrivateKey,
	amount float64,
	destination, token string,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":        "spotTransfer",
		"amount":      amount,
		"destination": destination,
		"token":       token,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignUsdTransferAction signs USD transfer action
func SignUsdTransferAction(
	privateKey *ecdsa.PrivateKey,
	amount float64,
	destination string,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":        "usdTransfer",
		"amount":      amount,
		"destination": destination,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignPerpDexClassTransferAction signs perp dex class transfer action
func SignPerpDexClassTransferAction(
	privateKey *ecdsa.PrivateKey,
	dex, token string,
	amount float64,
	toPerp bool,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":   "perpDexClassTransfer",
		"dex":    dex,
		"token":  token,
		"amount": amount,
		"toPerp": toPerp,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignTokenDelegateAction signs token delegate action
func SignTokenDelegateAction(
	privateKey *ecdsa.PrivateKey,
	token string,
	amount float64,
	validatorAddress string,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":             "tokenDelegate",
		"token":            token,
		"amount":           amount,
		"validatorAddress": validatorAddress,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignWithdrawFromBridgeAction signs withdraw from bridge action
func SignWithdrawFromBridgeAction(
	privateKey *ecdsa.PrivateKey,
	destination string,
	amount, fee float64,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":        "withdrawFromBridge",
		"destination": destination,
		"amount":      amount,
		"fee":         fee,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignAgent signs agent approval action
func SignAgent(
	privateKey *ecdsa.PrivateKey,
	agentAddress, agentName string,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":         "approveAgent",
		"agentAddress": agentAddress,
		"agentName":    agentName,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignApproveBuilderFee signs approve builder fee action
func SignApproveBuilderFee(
	privateKey *ecdsa.PrivateKey,
	builderAddress string,
	maxFeeRate float64,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":           "approveBuilderFee",
		"builderAddress": builderAddress,
		"maxFeeRate":     maxFeeRate,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignConvertToMultiSigUserAction signs convert to multi-sig user action
func SignConvertToMultiSigUserAction(
	privateKey *ecdsa.PrivateKey,
	signers []string,
	threshold int,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":      "convertToMultiSigUser",
		"signers":   signers,
		"threshold": threshold,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// SignMultiSigAction signs multi-signature action
func SignMultiSigAction(
	privateKey *ecdsa.PrivateKey,
	innerAction map[string]any,
	signers []string,
	signatures []string,
	timestamp int64,
	isMainnet bool,
) (SignatureResult, error) {
	action := map[string]any{
		"type":       "multiSig",
		"action":     innerAction,
		"signers":    signers,
		"signatures": signatures,
	}

	return SignL1Action(privateKey, action, "", timestamp, nil, isMainnet)
}

// Utility function to convert float to USD integer representation
func FloatToUsdInt(value float64) int {
	// Convert float USD to integer representation (assuming 6 decimals for USDC)
	return int(value * 1e6)
}

// GetTimestampMs returns current timestamp in milliseconds
func GetTimestampMs() int64 {
	return time.Now().UnixMilli()
}
