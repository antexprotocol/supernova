// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package consensus

// func TestConsensus(t *testing.T) {
// 	obValue := reflect.ValueOf(newTestConsensus(t))
// 	obType := obValue.Type()
// 	for i := 0; i < obValue.NumMethod(); i++ {
// 		if strings.HasPrefix(obType.Method(i).Name, "Test") {
// 			obValue.Method(i).Call(nil)
// 		}
// 	}
// }

// func txBuilder(tag byte) *tx.Builder {
// 	address := types.BytesToAddress([]byte("addr"))
// 	return new(tx.Builder).
// 		GasPriceCoef(1).
// 		Gas(1000000).
// 		Expiration(100).
// 		Clause(tx.NewClause(&address).WithValue(big.NewInt(10)).WithData(nil)).
// 		Nonce(1).
// 		ChainTag(tag)
// }

// func txSign(builder *tx.Builder) *tx.Transaction {
// 	transaction := builder.Build()
// 	sig, _ := crypto.Sign(transaction.SigningHash().Bytes(), genesis.DevAccounts()[0].PrivateKey)
// 	return transaction.WithSignature(sig)
// }

// type testConsensus struct {
// 	t        *testing.T
// 	assert   *assert.Assertions
// 	con      *Consensus
// 	time     uint64
// 	pk       *ecdsa.PrivateKey
// 	parent   *block.Block
// 	original *block.Block
// 	tag      byte
// }

// func newTestConsensus(t *testing.T) *testConsensus {
// 	db, err := lvldb.NewMem()
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	launchTime := uint64(1526400000)
// 	gen := new(genesis.Builder).
// 		GasLimit(types.InitialGasLimit).
// 		Timestamp(launchTime).
// 		State(func(state *state.State) error {
// 			bal, _ := new(big.Int).SetString("1000000000000000000000000000", 10)
// 			state.SetCode(builtin.Authority.Address, builtin.Authority.RuntimeBytecodes())
// 			builtin.Params.Native(state).Set(types.KeyExecutorAddress, new(big.Int).SetBytes(genesis.DevAccounts()[0].Address[:]))
// 			for _, acc := range genesis.DevAccounts() {
// 				state.SetBalance(acc.Address, bal)
// 				state.SetEnergy(acc.Address, bal, launchTime)
// 				builtin.Authority.Native(state).Add(acc.Address, acc.Address, types.Bytes32{})
// 			}
// 			return nil
// 		})

// 	stateCreator := state.NewCreator(db)
// 	parent, _, err := gen.Build(stateCreator)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	c, err := chain.New(db, parent)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	proposer := genesis.DevAccounts()[0]
// 	p := packer.New(c, stateCreator, proposer.Address, &proposer.Address)
// 	flow, err := p.Schedule(parent.Header(), uint64(time.Now().Unix()))
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	original, _, _, err := flow.Pack(proposer.PrivateKey)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	con := New(c, stateCreator)
// 	if _, _, err := con.Process(original, flow.When()); err != nil {
// 		t.Fatal(err)
// 	}

// 	return &testConsensus{
// 		t:        t,
// 		assert:   assert.New(t),
// 		con:      con,
// 		time:     flow.When(),
// 		pk:       proposer.PrivateKey,
// 		parent:   parent,
// 		original: original,
// 	}
// }

// func (tc *testConsensus) sign(blk *block.Block) *block.Block {
// 	sig, err := crypto.Sign(blk.Header().SigningHash().Bytes(), tc.pk)
// 	if err != nil {
// 		tc.t.Fatal(err)
// 	}
// 	return blk.WithSignature(sig)
// }

// func (tc *testConsensus) originalBuilder() *block.Builder {
// 	header := tc.original.Header()
// 	return new(block.Builder).
// 		ParentID(header.ParentID()).
// 		Timestamp(header.Timestamp()).
// 		TotalScore(header.TotalScore()).
// 		GasLimit(header.GasLimit()).
// 		GasUsed(header.GasUsed()).
// 		Beneficiary(header.Beneficiary()).
// 		StateRoot(header.StateRoot()).
// 		ReceiptsRoot(header.ReceiptsRoot())
// }

// func (tc *testConsensus) consent(blk *block.Block) error {
// 	_, _, err := tc.con.Process(blk, tc.time)
// 	return err
// }

// func (tc *testConsensus) TestValidateBlockHeader() {
// 	triggers := make(map[string]func())
// 	triggers["triggerErrTimestampBehindParent"] = func() {
// 		build := tc.originalBuilder()

// 		blk := tc.sign(build.Timestamp(tc.parent.Timestamp()).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block timestamp behind parents: parent %v, current %v",
// 				tc.parent.Timestamp(),
// 				blk.Timestamp(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)

// 		blk = tc.sign(build.Timestamp(tc.parent.Timestamp() - 1).Build())
// 		err = tc.consent(blk)
// 		expect = consensusError(
// 			fmt.Sprintf(
// 				"block timestamp behind parents: parent %v, current %v",
// 				tc.parent.Timestamp(),
// 				blk.Timestamp(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrInterval"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.Timestamp(tc.original.Timestamp() + 1).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block interval not rounded: parent %v, current %v",
// 				tc.parent.Timestamp(),
// 				blk.Timestamp(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrFutureBlock"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.Timestamp(tc.time + types.BlockInterval*2).Build())
// 		err := tc.consent(blk)
// 		tc.assert.Equal(err, errFutureBlock)
// 	}
// 	triggers["triggerInvalidGasLimit"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.GasLimit(tc.parent.GasLimit() * 2).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block gas limit invalid: parent %v, current %v",
// 				tc.parent.GasLimit(),
// 				blk.GasLimit(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerExceedGaUsed"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.GasUsed(tc.original.GasLimit() + 1).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block gas used exceeds limit: limit %v, used %v",
// 				tc.parent.GasLimit(),
// 				blk.Header().GasUsed(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerInvalidTotalScore"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.TotalScore(tc.parent.TotalScore()).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block total score invalid: parent %v, current %v",
// 				tc.parent.TotalScore(),
// 				blk.TotalScore(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}

// 	for _, trigger := range triggers {
// 		trigger()
// 	}
// }

// func (tc *testConsensus) TestTxDepBroken() {
// 	txID := txSign(txBuilder(tc.tag)).ID()
// 	tx := txSign(txBuilder(tc.tag).DependsOn(&txID))
// 	err := tc.consent(
// 		tc.sign(
// 			tc.originalBuilder().Transaction(tx).Build(),
// 		),
// 	)
// 	tc.assert.Equal(err, consensusError("tx dep broken"))
// }

// func (tc *testConsensus) TestKnownBlock() {
// 	err := tc.consent(tc.parent)
// 	tc.assert.Equal(err, errKnownBlock)
// }

// func (tc *testConsensus) TestTxAlreadyExists() {
// 	tx := txSign(txBuilder(tc.tag))
// 	err := tc.consent(
// 		tc.sign(
// 			tc.originalBuilder().Transaction(tx).Transaction(tx).Build(),
// 		),
// 	)
// 	tc.assert.Equal(err, consensusError("tx already exists"))
// }

// func (tc *testConsensus) TestParentMissing() {
// 	build := tc.originalBuilder()
// 	blk := tc.sign(build.ParentID(tc.original.ID()).Build())
// 	err := tc.consent(blk)
// 	tc.assert.Equal(err, errParentMissing)
// }

// func (tc *testConsensus) TestValidateBlockBody() {
// 	triggers := make(map[string]func())
// 	triggers["triggerErrTxSignerUnavailable"] = func() {
// 		blk := tc.sign(tc.originalBuilder().Transaction(txBuilder(tc.tag).Build()).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError("tx signer unavailable: invalid signature length")
// 		tc.assert.Equal(err, expect)
// 	}

// 	triggers["triggerErrTxsRootMismatch"] = func() {
// 		transaction := txSign(txBuilder(tc.tag))
// 		transactions := types.Transactions{transaction}
// 		blk := tc.sign(block.Compose(tc.original.Header(), transactions))
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block txs root mismatch: want %v, have %v",
// 				tc.original.Header().TxsRoot(),
// 				transactions.RootHash(),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrChainTagMismatch"] = func() {
// 		err := tc.consent(
// 			tc.sign(
// 				tc.originalBuilder().Transaction(
// 					txSign(txBuilder(tc.tag + 1)),
// 				).Build(),
// 			),
// 		)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"tx chain tag mismatch: want %v, have %v",
// 				tc.tag,
// 				tc.tag+1,
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrRefFutureBlock"] = func() {
// 		err := tc.consent(
// 			tc.sign(
// 				tc.originalBuilder().Transaction(
// 					txSign(txBuilder(tc.tag).BlockRef(tx.NewBlockRef(100))),
// 				).Build(),
// 			),
// 		)
// 		expect := consensusError("tx ref future block: ref 100, current 1")
// 		tc.assert.Equal(err, expect)
// 	}

// 	for _, trigger := range triggers {
// 		trigger()
// 	}
// }

// func (tc *testConsensus) TestValidateProposer() {
// 	triggers := make(map[string]func())
// 	triggers["triggerErrSignerUnavailable"] = func() {
// 		blk := tc.originalBuilder().Build()
// 		err := tc.consent(blk)
// 		expect := consensusError("block signer unavailable: invalid signature length")
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrSignerInvalid"] = func() {
// 		blk := tc.originalBuilder().Build()
// 		pk, _ := crypto.GenerateKey()
// 		sig, _ := crypto.Sign(blk.Header().SigningHash().Bytes(), pk)
// 		blk = blk.WithSignature(sig)
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block signer invalid: %v unauthorized block proposer",
// 				common.Address(crypto.PubkeyToAddress(pk.PublicKey)),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerErrTimestampUnscheduled"] = func() {
// 		blk := tc.originalBuilder().Build()
// 		sig, _ := crypto.Sign(blk.Header().SigningHash().Bytes(), genesis.DevAccounts()[1].PrivateKey)
// 		blk = blk.WithSignature(sig)
// 		err := tc.consent(blk)
// 		expect := consensusError(
// 			fmt.Sprintf(
// 				"block timestamp unscheduled: t %v, s %v",
// 				blk.Timestamp(),
// 				common.Address(crypto.PubkeyToAddress(genesis.DevAccounts()[1].PrivateKey.PublicKey)),
// 			),
// 		)
// 		tc.assert.Equal(err, expect)
// 	}
// 	triggers["triggerTotalScoreInvalid"] = func() {
// 		build := tc.originalBuilder()
// 		blk := tc.sign(build.TotalScore(tc.original.TotalScore() + 100).Build())
// 		err := tc.consent(blk)
// 		expect := consensusError("block total score invalid: want 1, have 101")
// 		tc.assert.Equal(err, expect)
// 	}

// 	for _, trigger := range triggers {
// 		trigger()
// 	}
// }
