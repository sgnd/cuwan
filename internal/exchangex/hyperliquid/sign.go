package hyperliquid

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// L1 actions are signed the way the official Python SDK does it
// (hyperliquid/utils/signing.py): msgpack the action, append the nonce and
// vault flag, keccak it, and sign that hash as an EIP-712 "Agent" message.
// signing_test.go checks this code against the SDK's own test vectors.

// kv is one map entry. Actions are ordered maps: msgpack keeps key order,
// and the hash depends on it.
type kv struct {
	K string
	V any
}

// omap is a msgpack map that keeps its key order.
type omap []kv

// MarshalJSON keeps the key order in the request body too.
func (m omap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, e := range m {
		if i > 0 {
			buf = append(buf, ',')
		}
		k, err := json.Marshal(e.K)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(e.V)
		if err != nil {
			return nil, err
		}
		buf = append(append(append(buf, k...), ':'), v...)
	}
	return append(buf, '}'), nil
}

// pack encodes v the way Python's msgpack.packb does for the types an
// action uses: ordered maps, lists, strings, bools, ints and nil.
func pack(buf []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(buf, 0xc0), nil
	case bool:
		if x {
			return append(buf, 0xc3), nil
		}
		return append(buf, 0xc2), nil
	case string:
		n := len(x)
		switch {
		case n < 32:
			buf = append(buf, 0xa0|byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
		case n < 1<<8:
			buf = append(buf, 0xd9, byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
		case n < 1<<16:
			buf = append(buf, 0xda, byte(n>>8), byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
		case n <= math.MaxUint32:
			buf = append(buf, 0xdb)
			buf = binary.BigEndian.AppendUint32(buf, uint32(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
		default:
			return nil, fmt.Errorf("hyperliquid: string of %d bytes is too long to sign", n)
		}
		return append(buf, x...), nil
	case int:
		return packInt(buf, int64(x)), nil // an int always fits an int64
	case int64:
		return packInt(buf, x), nil
	case []any:
		if len(x) >= 1<<16 {
			return nil, fmt.Errorf("hyperliquid: array of %d items is too long to sign", len(x))
		}
		buf = packLen(buf, len(x), 0x90, 0xdc)
		for _, e := range x {
			var err error
			if buf, err = pack(buf, e); err != nil {
				return nil, err
			}
		}
		return buf, nil
	case omap:
		if len(x) >= 1<<16 {
			return nil, fmt.Errorf("hyperliquid: map of %d keys is too long to sign", len(x))
		}
		buf = packLen(buf, len(x), 0x80, 0xde)
		for _, e := range x {
			var err error
			if buf, err = pack(buf, e.K); err != nil {
				return nil, err
			}
			if buf, err = pack(buf, e.V); err != nil {
				return nil, err
			}
		}
		return buf, nil
	}
	return nil, fmt.Errorf("hyperliquid: cannot pack %T", v)
}

// packLen writes a fix or 16-bit array/map header; pack refuses n >= 65536.
func packLen(buf []byte, n int, fix, b16 byte) []byte {
	if n < 16 {
		return append(buf, fix|byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	}
	return append(buf, b16, byte(n>>8), byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
}

// packInt uses the smallest encoding, as Python's msgpack does: positive
// values as unsigned, negative values as signed.
func packInt(buf []byte, n int64) []byte {
	switch {
	case n >= 0 && n < 128:
		return append(buf, byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= 0 && n < 1<<8:
		return append(buf, 0xcc, byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= 0 && n < 1<<16:
		return append(buf, 0xcd, byte(n>>8), byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= 0 && n < 1<<32:
		buf = append(buf, 0xce)
		return binary.BigEndian.AppendUint32(buf, uint32(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= 0:
		buf = append(buf, 0xcf)
		return binary.BigEndian.AppendUint64(buf, uint64(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= -32:
		return append(buf, byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= math.MinInt8:
		return append(buf, 0xd0, byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= math.MinInt16:
		return append(buf, 0xd1, byte(n>>8), byte(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	case n >= math.MinInt32:
		buf = append(buf, 0xd2)
		return binary.BigEndian.AppendUint32(buf, uint32(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
	}
	buf = append(buf, 0xd3)
	return binary.BigEndian.AppendUint64(buf, uint64(n)) // #nosec G115 -- the case above bounds n to this width; msgpack keeps the low bytes on purpose
}

func keccak(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// actionHash is the SDK's action_hash with no vault and no expiry.
func actionHash(action omap, nonce uint64) ([]byte, error) {
	data, err := pack(nil, action)
	if err != nil {
		return nil, err
	}
	data = binary.BigEndian.AppendUint64(data, nonce)
	data = append(data, 0x00) // no vault address
	return keccak(data), nil
}

var (
	domainType = keccak([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	agentType  = keccak([]byte("Agent(string source,bytes32 connectionId)"))
	// chainId 1337 and the zero contract are fixed for L1 actions.
	domainSep = keccak(domainType, keccak([]byte("Exchange")), keccak([]byte("1")),
		new(big.Int).SetInt64(1337).FillBytes(make([]byte, 32)), make([]byte, 32))
)

// agentDigest is the EIP-712 digest of the phantom agent for this action.
// source "a" is mainnet, "b" is testnet.
func agentDigest(connectionID []byte, mainnet bool) []byte {
	source := "b"
	if mainnet {
		source = "a"
	}
	structHash := keccak(agentType, keccak([]byte(source)), connectionID)
	return keccak([]byte{0x19, 0x01}, domainSep, structHash)
}

// Signature is the wire form the exchange endpoint takes.
type Signature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

// signer holds a wallet key and its address.
type signer struct {
	key  *secp256k1.PrivateKey
	addr string
}

func newSigner(hexKey string) (*signer, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexKey, "0x"))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("hyperliquid: the secret must be a 32-byte hex private key")
	}
	// The key must be in [1, n-1]; PrivKeyFromBytes would reduce it silently.
	var n secp256k1.ModNScalar
	if overflow := n.SetByteSlice(b); overflow || n.IsZero() {
		return nil, fmt.Errorf("hyperliquid: the secret is not a valid private key")
	}
	k := secp256k1.NewPrivateKey(&n)
	pub := k.PubKey().SerializeUncompressed()
	return &signer{key: k, addr: "0x" + hex.EncodeToString(keccak(pub[1:])[12:])}, nil
}

// signL1 signs an L1 action. RFC 6979 nonces and low-S match eth_account.
func (s *signer) signL1(action omap, nonce uint64, mainnet bool) (Signature, error) {
	h, err := actionHash(action, nonce)
	if err != nil {
		return Signature{}, err
	}
	sig := ecdsa.SignCompact(s.key, agentDigest(h, mainnet), false) // [27+recid][r][s]
	return Signature{
		R: "0x" + new(big.Int).SetBytes(sig[1:33]).Text(16),
		S: "0x" + new(big.Int).SetBytes(sig[33:65]).Text(16),
		V: int(sig[0]),
	}, nil
}

// floatToWire is the SDK's float_to_wire: 8 decimals, trailing zeros cut,
// and an error when that would change the value.
func floatToWire(x float64) (string, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "", fmt.Errorf("hyperliquid: %v is not a number", x)
	}
	s := strconv.FormatFloat(x, 'f', 8, 64)
	if r, _ := strconv.ParseFloat(s, 64); math.Abs(r-x) >= 1e-12 {
		return "", fmt.Errorf("hyperliquid: %v needs more than 8 decimals", x)
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		s = "0"
	}
	return s, nil
}

// orderWire is the SDK's order_request_to_order_wire for a limit order.
func orderWire(asset int, buy bool, px, sz float64, reduceOnly bool, tif, cloid string) (omap, error) {
	if !(px > 0) || !(sz > 0) {
		return nil, fmt.Errorf("hyperliquid: price %v and size %v must be positive", px, sz)
	}
	p, err := floatToWire(px)
	if err != nil {
		return nil, err
	}
	q, err := floatToWire(sz)
	if err != nil {
		return nil, err
	}
	w := omap{
		{"a", asset}, {"b", buy}, {"p", p}, {"s", q}, {"r", reduceOnly},
		{"t", omap{{"limit", omap{{"tif", tif}}}}},
	}
	if cloid != "" {
		w = append(w, kv{"c", cloid})
	}
	return w, nil
}
