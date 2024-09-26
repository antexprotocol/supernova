// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package tx

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/params"
)

var (
	errIntrinsicGasOverflow = errors.New("intrinsic gas overflow")
	RESERVED_PREFIX         = []byte{0xee, 0xff}
)

// reference to github.com/ethereum/go-ethereum/
func isProtectedV(V *big.Int) bool {
	if V.BitLen() <= 8 {
		v := V.Uint64()
		return v != 27 && v != 28
	}
	// anything not 27 or 28 is considered protected
	return true
}

// deriveChainId derives the chain id from the given v parameter
func deriveChainId(v *big.Int) *big.Int {
	if v.BitLen() <= 64 {
		v := v.Uint64()
		if v == 27 || v == 28 {
			return new(big.Int)
		}
		return new(big.Int).SetUint64((v - 35) / 2)
	}
	v = new(big.Int).Sub(v, big.NewInt(35))
	return v.Div(v, big.NewInt(2))
}

func recoverPlain(sighash common.Hash, R, S, V *big.Int, homestead bool) (common.Address, error) {
	var y byte
	if isProtectedV(V) {
		chainID := deriveChainId(V).Uint64()
		y = byte(V.Uint64() - 35 - 2*chainID)
	} else {
		y = byte(V.Uint64() - 27)
	}

	if !crypto.ValidateSignatureValues(y, R, S, homestead) {
		return common.Address{}, types.ErrInvalidSig
	}

	// encode the snature in uncompressed format
	r, s := R.Bytes(), S.Bytes()
	sig := make([]byte, 65)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):64], s)
	sig[64] = y
	// recover the public key from the snature
	pub, err := crypto.Ecrecover(sighash[:], sig)
	if err != nil {
		return common.Address{}, err
	}
	if len(pub) == 0 || pub[0] != 4 {
		return common.Address{}, errors.New("invalid public key")
	}
	var addr common.Address
	copy(addr[:], crypto.Keccak256(pub[1:])[12:])
	return addr, nil
}

// Transaction is an immutable tx type.
type Transaction struct {
	body body

	cache struct {
		signingHash  atomic.Value
		signer       atomic.Value
		id           atomic.Value
		unprovedWork atomic.Value
		size         atomic.Uint64
		intrinsicGas atomic.Value
	}
}

// body describes details of a tx.
type body struct {
	ChainTag     byte
	BlockRef     uint64
	Expiration   uint32
	Clauses      []*Clause
	GasPriceCoef uint8
	Gas          uint64
	DependsOn    *meter.Bytes32 `rlp:"nil"`
	Nonce        uint64
	Reserved     []interface{}
	Signature    []byte
}

func NewTransactionFromEthTx(ethTx *types.Transaction, chainTag byte, blockRef BlockRef, verbose bool) (*Transaction, error) {
	signer := types.NewLondonSigner(ethTx.ChainId())

	ethFrom, err := types.Sender(signer, ethTx)
	from, err := meter.ParseAddress(ethFrom.Hex())
	if err != nil {
		return nil, err
	}
	if strings.ToLower(from.String()) == "0x0e369a2e02912dba872e72d6c0b661e9617e0d9c" {
		return nil, errors.New("blacklisted address, not allowed")
	}
	to := meter.Address{}
	if ethTx.To() != nil {
		to, err = meter.ParseAddress(ethTx.To().Hex())
		if err != nil {
			return nil, err
		}
	}

	raw, err := ethTx.MarshalBinary()
	if err != nil {
		return nil, err
	}

	// collect signature from ethereum tx
	V, R, S := ethTx.RawSignatureValues()
	var rawEthTx bytes.Buffer
	err = ethTx.EncodeRLP(&rawEthTx)
	if err != nil {
		return nil, err
	}

	// padding 0 for S to 32bytes
	sBytes := make([]byte, 0)
	if len(S.Bytes()) < 32 {
		for i := 0; i < 32-len(S.Bytes()); i++ {
			sBytes = append(sBytes, 0)
		}
	}
	sBytes = append(sBytes, S.Bytes()...)

	// padding 0 for R to 32bytes
	rBytes := make([]byte, 0)
	if len(R.Bytes()) < 32 {
		for i := 0; i < 32-len(R.Bytes()); i++ {
			rBytes = append(rBytes, 0)
		}
	}
	rBytes = append(rBytes, R.Bytes()...)

	ethSignature := append(append(rBytes, sBytes...), V.Bytes()...)
	value := ethTx.Value()

	sender, err := signer.Sender(ethTx)
	if err != nil {
		return nil, err
	}

	if !strings.EqualFold(sender.Hex(), from.String()) {
		fmt.Println("ethereum sender != from, this tx uses homestead style")
		fmt.Println("origin: ", sender.Hex())
		fmt.Println("from: ", from.String())
		return nil, errors.New("invalid ethereum tx: origin is not the same as from")
	}
	var actualTo *meter.Address
	if to.String() == "0x0000000000000000000000000000000000000000" {
		actualTo = nil
	} else {
		actualTo = &to
	}
	tx := &Transaction{
		body: body{
			ChainTag:     chainTag,
			BlockRef:     blockRef.Uint64(),
			Expiration:   320,
			Clauses:      []*Clause{&Clause{body: clauseBody{To: actualTo, Value: value, Token: meter.MTR, Data: ethTx.Data()}}},
			GasPriceCoef: 0,
			Gas:          ethTx.Gas(),
			DependsOn:    nil,
			Nonce:        ethTx.Nonce(),
			Reserved:     []interface{}{RESERVED_PREFIX, ethFrom.Bytes(), raw},
			Signature:    ethSignature,
		},
	}

	if !strings.EqualFold(tx.ID().String(), ethTx.Hash().Hex()) {
		return nil, errors.New("tx.id not matching ethTx.Hash")
	}
	if verbose {
		slog.Debug("new nativeTx from ethTx", "id", tx.ID(), "from", ethFrom.Hex(), "to", to.String())
	}
	return tx, nil
}

func (t *Transaction) IsEthTx() bool {
	reserved3 := len(t.body.Reserved) == 3
	if !reserved3 {
		return false
	}

	reservedPrefixSameLen := len(t.body.Reserved[0].([]byte)) == len(RESERVED_PREFIX)
	reservedPrefixEqual := hex.EncodeToString(t.body.Reserved[0].([]byte)) == hex.EncodeToString(RESERVED_PREFIX[:])
	sigLen65 := len(t.body.Signature) >= 65
	sigLen64 := len(t.body.Signature) >= 64
	typedTx := t.body.Reserved[2].([]byte)[0] > 0 && t.body.Reserved[2].([]byte)[0] < 3 // only 0x01 and 0x02 is alllowed
	return reserved3 && reservedPrefixSameLen && reservedPrefixEqual && ((typedTx && sigLen64) || sigLen65)

}

func (t *Transaction) GetEthTx() (*types.Transaction, error) {
	if !t.IsEthTx() {
		return nil, errors.New("not a tx from ethereum")
	}
	var ethTx types.Transaction
	rawEthTx := t.body.Reserved[2].([]byte)
	err := ethTx.UnmarshalBinary(rawEthTx)
	if err != nil {
		return nil, err
	}
	return &ethTx, nil
}

func (t *Transaction) Type() byte {
	if t.IsEthTx() && len(t.body.Reserved) >= 3 {
		rawEthTx := t.body.Reserved[2].([]byte)
		if rawEthTx[0] == byte(1) {
			return byte(1)
		} else if rawEthTx[0] == byte(2) {
			return byte(2)
		}
	}
	return byte(0)
}

// ChainTag returns chain tag.
func (t *Transaction) ChainTag() byte {
	return t.body.ChainTag
}

func (t *Transaction) Reserved() []interface{} {
	return t.body.Reserved
}

// Nonce returns nonce value.
func (t *Transaction) Nonce() uint64 {
	return t.body.Nonce
}

// BlockRef returns block reference, which is first 8 bytes of block hash.
func (t *Transaction) BlockRef() (br BlockRef) {
	binary.BigEndian.PutUint64(br[:], t.body.BlockRef)
	return
}

// Expiration returns expiration in unit block.
// A valid transaction requires:
// blockNum in [blockRef.Num... blockRef.Num + Expiration]
func (t *Transaction) Expiration() uint32 {
	return t.body.Expiration
}

// IsExpired returns whether the tx is expired according to the given blockNum.
func (t *Transaction) IsExpired(blockNum uint32) bool {
	return uint64(blockNum) > uint64(t.BlockRef().Number())+uint64(t.body.Expiration) // cast to uint64 to prevent potential overflow
}

func ChainIdValidate(chainId *big.Int) (bool, error) {
	if meter.IsMainNet() && chainId.Cmp(new(big.Int).SetUint64(meter.MainnetChainID)) != 0 {
		return false, errors.New(fmt.Sprintf("wrong mainNet chainId %v", chainId))
	}
	if meter.IsTestNet() && chainId.Cmp(new(big.Int).SetUint64(meter.TestnetChainID)) != 0 {
		return false, errors.New(fmt.Sprintf("wrong testNet chainId %v", chainId))
	}

	return true, nil
}

func (t *Transaction) EthTxValidate() (bool, error) {
	var ethTx *types.Transaction
	var err error
	var reverseTx *Transaction

	if t.IsEthTx() {
		ethTx, err = t.GetEthTx()
		if err != nil {
			return false, err
		}

		if _, err = ChainIdValidate(ethTx.ChainId()); err != nil {
			return false, err
		}

		if reverseTx, err = NewTransactionFromEthTx(ethTx, t.ChainTag(), t.BlockRef(), false); err != nil {
			return false, err
		}

		txID := t.MeterID()
		reverseTxID := reverseTx.MeterID()
		if bytes.Compare(txID[:], reverseTxID[:]) != 0 {
			return false, errors.New(fmt.Sprintf("reverseTx err, txID %v", t.ID()))
		}
	}

	return true, nil
}

// ID returns id of tx.
// ID = hash(signingHash, signer).
// It returns zero Bytes32 if signer not available.
func (t *Transaction) ID() (id meter.Bytes32) {
	if t.IsEthTx() {
		if meter.IsTesla(t.BlockRef().Number()) {
			ethTx, err := t.GetEthTx()
			if err != nil {
				return meter.Bytes32{}
			}
			hash := ethTx.Hash()
			id, err := meter.ParseBytes32(hash.String())
			if err != nil {
				return meter.Bytes32{}
			}
			return id
		}
	}
	return t.MeterID()
}

func (t *Transaction) MeterID() (id meter.Bytes32) {
	if cached := t.cache.id.Load(); cached != nil {
		return cached.(meter.Bytes32)
	}
	defer func() { t.cache.id.Store(id) }()

	signer, err := t.Signer()
	if err != nil {
		return
	}
	hw := meter.NewBlake2b()
	hw.Write(t.SigningHash().Bytes())
	hw.Write(signer.Bytes())
	hw.Sum(id[:0])
	return
}

// UnprovedWork returns unproved work of this tx.
// It returns 0, if tx is not signed.
func (t *Transaction) UnprovedWork() (w *big.Int) {
	if cached := t.cache.unprovedWork.Load(); cached != nil {
		return cached.(*big.Int)
	}
	defer func() {
		t.cache.unprovedWork.Store(w)
	}()

	signer, err := t.Signer()
	if err != nil {
		return &big.Int{}
	}
	return t.EvaluateWork(signer)(t.body.Nonce)
}

// EvaluateWork try to compute work when tx signer assumed.
func (t *Transaction) EvaluateWork(signer meter.Address) func(nonce uint64) *big.Int {
	hw := meter.NewBlake2b()
	err := rlp.Encode(hw, []interface{}{
		t.body.ChainTag,
		t.body.BlockRef,
		t.body.Expiration,
		t.body.Clauses,
		t.body.GasPriceCoef,
		t.body.Gas,
		t.body.DependsOn,
		t.body.Reserved,
		signer,
	})
	if err != nil {
		return nil
	}

	var hashWithoutNonce meter.Bytes32
	hw.Sum(hashWithoutNonce[:0])

	return func(nonce uint64) *big.Int {
		var nonceBytes [8]byte
		binary.BigEndian.PutUint64(nonceBytes[:], nonce)
		hash := meter.Blake2b(hashWithoutNonce[:], nonceBytes[:])
		r := new(big.Int).SetBytes(hash[:])
		return r.Div(math.MaxBig256, r)
	}
}

// SigningHash returns hash of tx excludes signature.
func (t *Transaction) SigningHash() (hash meter.Bytes32) {
	if cached := t.cache.signingHash.Load(); cached != nil {
		return cached.(meter.Bytes32)
	}
	defer func() { t.cache.signingHash.Store(hash) }()

	hw := meter.NewBlake2b()
	err := rlp.Encode(hw, []interface{}{
		t.body.ChainTag,
		t.body.BlockRef,
		t.body.Expiration,
		t.body.Clauses,
		t.body.GasPriceCoef,
		t.body.Gas,
		t.body.DependsOn,
		t.body.Nonce,
		t.body.Reserved,
	})
	if err != nil {
		return
	}

	hw.Sum(hash[:0])
	return
}

func (t *Transaction) UniteHash() (hash meter.Bytes32) {
	hw := meter.NewBlake2b()
	err := rlp.Encode(hw, []interface{}{
		t.body.ChainTag,
		//t.body.BlockRef,
		t.body.Expiration,
		//t.body.Clauses,
		t.body.GasPriceCoef,
		//t.body.Gas,
		t.body.DependsOn,
		t.body.Nonce,
		t.body.Reserved,
		//t.body.Signature,
	})
	if err != nil {
		return
	}

	hw.Sum(hash[:0])
	return
}

// GasPriceCoef returns gas price coef.
// gas price = bgp + bgp * gpc / 255.
func (t *Transaction) GasPriceCoef() uint8 {
	return t.body.GasPriceCoef
}

// Gas returns gas provision for this tx.
func (t *Transaction) Gas() uint64 {
	return t.body.Gas
}

// Clauses returns caluses in tx.
func (t *Transaction) Clauses() []*Clause {
	return append([]*Clause(nil), t.body.Clauses...)
}

// DependsOn returns depended tx hash.
func (t *Transaction) DependsOn() *meter.Bytes32 {
	if t.body.DependsOn == nil {
		return nil
	}
	cpy := *t.body.DependsOn
	return &cpy
}

// Signature returns signature.
func (t *Transaction) Signature() []byte {
	return append([]byte(nil), t.body.Signature...)
}

// Signer extract signer of tx from signature.
func (t *Transaction) Signer() (signer meter.Address, err error) {
	// set the origin to nil if no signature
	if len(t.body.Signature) == 0 {
		return meter.Address{}, nil
	}
	if t.IsEthTx() {
		// ethereum translated tx
		from := "0x" + hex.EncodeToString(t.body.Reserved[1].([]byte))
		addr, err := meter.ParseAddress(from)
		return addr, err
	}

	if cached := t.cache.signer.Load(); cached != nil {
		return cached.(meter.Address), nil
	}
	defer func() {
		if err == nil {
			t.cache.signer.Store(signer)
		}
	}()

	pub, err := crypto.SigToPub(t.SigningHash().Bytes(), t.body.Signature)
	if err != nil {
		return meter.Address{}, err
	}
	signer = meter.Address(crypto.PubkeyToAddress(*pub))
	return
}

// WithSignature create a new tx with signature set.
func (t *Transaction) WithSignature(sig []byte) *Transaction {
	newTx := Transaction{
		body: t.body,
	}
	// copy sig
	newTx.body.Signature = append([]byte(nil), sig...)
	return &newTx
}

// HasReservedFields returns if there're reserved fields.
// Reserved fields are for backward compatibility purpose.
func (t *Transaction) HasReservedFields() bool {
	return len(t.body.Reserved) > 0
}

// EncodeRLP implements rlp.Encoder
func (t *Transaction) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &t.body)
}

// DecodeRLP implements rlp.Decoder
func (t *Transaction) DecodeRLP(s *rlp.Stream) error {
	_, size, err := s.Kind()
	if err != nil {
		return err
	}
	var body body
	if err := s.Decode(&body); err != nil {
		return err
	}
	*t = Transaction{body: body}

	t.cache.size.Store(rlp.ListSize(size))
	return nil
}

type writeCounter uint64

func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}

// Size returns the true encoded storage size of the transaction, either by encoding
// and returning it, or returning a previously cached value.
func (tx *Transaction) Size() uint64 {
	if size := tx.cache.size.Load(); size > 0 {
		return size
	}

	// Cache miss, encode and cache.
	// Note we rely on the assumption that all tx.inner values are RLP-encoded!
	c := writeCounter(0)
	err := rlp.Encode(&c, &tx)
	if err != nil {
		fmt.Printf("rlp failed: %s\n", err.Error())
		return 0
	}
	size := uint64(c)

	tx.cache.size.Store(size)
	return size
}

// IntrinsicGas returns intrinsic gas of tx.
func (t *Transaction) IntrinsicGas() (uint64, error) {
	if cached := t.cache.intrinsicGas.Load(); cached != nil {
		return cached.(uint64), nil
	}
	isEIP2028 := meter.IsTeslaFork11(t.BlockRef().Number())
	isEIP3860 := meter.IsTeslaFork11(t.BlockRef().Number())

	gas, err := IntrinsicGas(isEIP2028, isEIP3860, t.body.Clauses...)
	if err != nil {
		return 0, err
	}

	t.cache.intrinsicGas.Store(gas)
	return gas, nil
}

// GasPrice returns gas price.
// gasPrice = baseGasPrice + baseGasPrice * gasPriceCoef / 255
func (t *Transaction) GasPrice(baseGasPrice *big.Int) *big.Int {
	x := big.NewInt(int64(t.body.GasPriceCoef))
	x.Mul(x, baseGasPrice)
	x.Div(x, big.NewInt(math.MaxUint8))
	return x.Add(x, baseGasPrice)
}

// ProvedWork returns proved work.
// Unproved work will be considered as proved work if block ref is do the prefix of a block's ID,
// and tx delay is less equal to MaxTxWorkDelay.
func (t *Transaction) ProvedWork(headBlockNum uint32, getBlockID func(uint32) meter.Bytes32) *big.Int {
	ref := t.BlockRef()
	refNum := ref.Number()
	if refNum >= headBlockNum {
		return &big.Int{}
	}

	if delay := headBlockNum - refNum; delay > meter.MaxTxWorkDelay {
		return &big.Int{}
	}

	id := getBlockID(refNum)
	if bytes.HasPrefix(id[:], ref[:]) {
		return t.UnprovedWork()
	}
	return &big.Int{}
}

// OverallGasPrice calculate overall gas price.
// overallGasPrice = gasPrice + baseGasPrice * wgas/gas.
func (t *Transaction) OverallGasPrice(baseGasPrice *big.Int, headBlockNum uint32, getBlockID func(uint32) meter.Bytes32) *big.Int {
	gasPrice := t.GasPrice(baseGasPrice)

	provedWork := t.ProvedWork(headBlockNum, getBlockID)
	if provedWork.Sign() == 0 {
		return gasPrice
	}

	wgas := workToGas(provedWork, t.BlockRef().Number())
	if wgas == 0 {
		return gasPrice
	}
	if wgas > t.body.Gas {
		wgas = t.body.Gas
	}

	x := new(big.Int).SetUint64(wgas)
	x.Mul(x, baseGasPrice)
	x.Div(x, new(big.Int).SetUint64(t.body.Gas))
	return x.Add(x, gasPrice)
}

func (t *Transaction) String() string {
	var (
		from      string
		br        BlockRef
		dependsOn string
	)
	signer, err := t.Signer()
	if err != nil {
		from = "N/A"
	} else {
		from = signer.String()
	}

	binary.BigEndian.PutUint64(br[:], t.body.BlockRef)
	if t.body.DependsOn == nil {
		dependsOn = ""
	} else {
		dependsOn = fmt.Sprintf("DependsOn: %v", t.body.DependsOn.String())
	}

	suffix := ""
	if t.IsEthTx() {
		suffix = "(from EthTx)"
	}

	return fmt.Sprintf(`
Tx %v %v %v %v:
    From:               %v
    ClauseCount:        %v
    GasPriceCoef / Gas: %v / %v
    ChainTag / Nonce:   %v / %v
    BlockRef / Exp:     %v-%x / %v
    Signature:          0x%x`, t.ID(), t.Size(), suffix, dependsOn, from, len(t.body.Clauses), t.body.GasPriceCoef, t.body.Gas,
		t.body.ChainTag, t.body.Nonce, br.Number(), br[4:], t.body.Expiration, t.body.Signature)
}

// toWordSize returns the ceiled word size required for init code payment calculation.
func toWordSize(size uint64) uint64 {
	if size > math.MaxUint64-31 {
		return math.MaxUint64/32 + 1
	}

	return (size + 31) / 32
}

// IntrinsicGas calculate intrinsic gas cost for tx with such clauses.
func IntrinsicGas(isEIP2028 bool, isEIP3860 bool, clauses ...*Clause) (uint64, error) {
	if len(clauses) == 0 {
		return meter.TxGas + meter.ClauseGas, nil
	}

	var total = meter.TxGas
	var overflow bool
	for _, c := range clauses {
		gas, err := dataGas(c.body.Data, isEIP2028)
		if err != nil {
			return 0, err
		}
		total, overflow = math.SafeAdd(total, gas)
		if overflow {
			return 0, errIntrinsicGasOverflow
		}

		var cgas uint64
		isCreatingContract := c.IsCreatingContract()
		if isCreatingContract {
			// contract creation
			cgas = meter.ClauseGasContractCreation
		} else {
			cgas = meter.ClauseGas
		}

		total, overflow = math.SafeAdd(total, cgas)
		if overflow {
			return 0, errIntrinsicGasOverflow
		}

		if isCreatingContract && isEIP3860 {
			lenWords := toWordSize(uint64(len(c.Data())))
			if (math.MaxUint64-gas)/params.InitCodeWordGas < lenWords {
				return 0, errIntrinsicGasOverflow
			}
			gas += lenWords * params.InitCodeWordGas
		}

	}
	return total, nil
}

// see core.IntrinsicGas
func dataGas(data []byte, isEIP2028 bool) (uint64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var z, nz uint64
	for _, byt := range data {
		if byt == 0 {
			z++
		} else {
			nz++
		}
	}
	zgas, overflow := math.SafeMul(params.TxDataZeroGas, z)
	if overflow {
		return 0, errIntrinsicGasOverflow
	}
	nonZeroGas := params.TxDataNonZeroGas
	if isEIP2028 {
		nonZeroGas = params.TxDataNonZeroGasEIP2028
	}
	nzgas, overflow := math.SafeMul(nonZeroGas, nz)
	if overflow {
		return 0, errIntrinsicGasOverflow
	}

	gas, overflow := math.SafeAdd(zgas, nzgas)
	if overflow {
		return 0, errIntrinsicGasOverflow
	}
	return gas, nil
}
