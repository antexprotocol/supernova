package fork9

import (
	"fmt"
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/meterio/meter-pov/block"
	"github.com/meterio/meter-pov/builtin"
	"github.com/meterio/meter-pov/chain"
	"github.com/meterio/meter-pov/genesis"
	"github.com/meterio/meter-pov/lvldb"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/packer"
	"github.com/meterio/meter-pov/runtime"
	"github.com/meterio/meter-pov/runtime/statedb"
	"github.com/meterio/meter-pov/script"
	"github.com/meterio/meter-pov/script/staking"
	"github.com/meterio/meter-pov/state"
	"github.com/meterio/meter-pov/tests"
	"github.com/meterio/meter-pov/xenv"
)

func initRuntimeAfterFork9() *tests.TestEnv {
	tests.InitLogger()
	kv, _ := lvldb.NewMem()
	meter.InitBlockChainConfig("main")
	// ts := uint64(time.Now().Unix()) - meter.MIN_CANDIDATE_UPDATE_INTV - 1

	b0 := tests.BuildGenesis(kv, func(state *state.State) error {
		state.SetCode(builtin.Prototype.Address, builtin.Prototype.RuntimeBytecodes())
		state.SetCode(builtin.Executor.Address, builtin.Executor.RuntimeBytecodes())
		state.SetCode(builtin.Params.Address, builtin.Params.RuntimeBytecodes())
		state.SetCode(builtin.Measure.Address, builtin.Measure.RuntimeBytecodes())
		builtin.Params.Native(state).Set(meter.KeyExecutorAddress, new(big.Int).SetBytes(builtin.Executor.Address[:]))

		// init MTRG sys contract
		state.SetCode(tests.MTRGSysContractAddr, builtin.MeterGovERC20Permit_DeployedBytecode)
		state.SetStorage(tests.MTRGSysContractAddr, meter.BytesToBytes32([]byte{1}), meter.BytesToBytes32(builtin.MeterTracker.Address[:]))
		builtin.Params.Native(state).SetAddress(meter.KeySystemContractAddress1, tests.MTRGSysContractAddr)

		// MeterTracker / ScriptEngine will be initialized on fork9

		// testing env set up like this:
		// 2 candidates: Cand, Cand2
		// 3 votes: Cand->Cand(self, Cand2->Cand2(self), Voter2->Cand

		// init candidate list & bucket list
		sdb := statedb.New(state)

		// init balance for candidates
		sdb.MintBalance(common.Address(tests.CandAddr), tests.BuildAmount(2000))
		sdb.MintEnergy(common.Address(tests.CandAddr), tests.BuildAmount(100))
		sdb.MintBalance(common.Address(tests.Cand2Addr), tests.BuildAmount(2000))
		sdb.MintEnergy(common.Address(tests.Cand2Addr), tests.BuildAmount(100))

		// init balance for holders
		sdb.MintBalance(common.Address(tests.HolderAddr), tests.BuildAmount(1200))
		sdb.MintEnergy(common.Address(tests.HolderAddr), tests.BuildAmount(100))

		// init balance for voters
		sdb.MintBalance(common.Address(tests.VoterAddr), tests.BuildAmount(3000))
		sdb.MintEnergy(common.Address(tests.VoterAddr), tests.BuildAmount(100))
		sdb.MintBalance(common.Address(tests.Voter2Addr), tests.BuildAmount(1500))
		sdb.MintEnergy(common.Address(tests.Voter2Addr), tests.BuildAmount(100))
		sdb.MintBalance(common.Address(tests.VoterAddr), tests.BuildAmount(2000000))

		sdb.MintEnergy(common.Address(tests.Voter2Addr), tests.BuildAmount(1234))
		sdb.BurnEnergy(common.Address(tests.Voter2Addr), tests.BuildAmount(1234))

		// disable previous fork corrections
		builtin.Params.Native(state).Set(meter.KeyEnforceTesla1_Correction, big.NewInt(1))
		builtin.Params.Native(state).Set(meter.KeyEnforceTesla5_Correction, big.NewInt(1))
		builtin.Params.Native(state).Set(meter.KeyEnforceTesla_Fork6_Correction, big.NewInt(1))

		// load SampleStakingPool for testing
		state.SetCode(tests.SampleStakingPoolAddr, tests.SampleStakingPool_DeployedBytes)
		state.SetStorage(tests.SampleStakingPoolAddr, meter.BytesToBytes32([]byte{0}), meter.BytesToBytes32(meter.ScriptEngineSysContractAddr[:]))
		state.SetStorage(tests.SampleStakingPoolAddr, meter.BytesToBytes32([]byte{1}), meter.BytesToBytes32(tests.MTRGSysContractAddr[:]))
		state.SetEnergy(tests.SampleStakingPoolAddr, tests.BuildAmount(100))
		state.SetBalance(tests.SampleStakingPoolAddr, tests.BuildAmount(200))
		return nil
	})
	b0.SetQC(&block.QuorumCert{QCHeight: 0, QCRound: 0, EpochID: 0, VoterBitArrayStr: "X_XXX", VoterMsgHash: meter.BytesToBytes32([]byte("hello")), VoterAggSig: []byte("voteraggr")})
	// fmt.Println(b0.ID())
	c, _ := chain.New(kv, b0, false)
	seeker := c.NewSeeker(b0.ID())
	sc := state.NewCreator(kv)
	se := script.NewScriptEngine(c, sc)
	se.StartTeslaForkModules()

	currentTs := uint64(time.Now().Unix())
	packer := packer.New(c, sc, genesis.DevAccounts()[0].Address, &genesis.DevAccounts()[0].Address)
	flow, err := packer.Mock(b0.Header(), currentTs, 2000000, &meter.Address{})
	if err != nil {
		panic(err)
	}
	tx1 := tests.BuildCandidateTxForCand(c.Tag(), 2000)
	if err = flow.Adopt(tx1); err != nil {
		panic(err)
	}
	tx2 := tests.BuildCandidateTxForCand2(c.Tag(), 2000)
	if err = flow.Adopt(tx2); err != nil {
		panic(err)
	}
	tx3 := tests.BuildVoteTx(c.Tag(), tests.Voter2Key, tests.Voter2Addr, tests.Cand2Addr, tests.BuildAmount(500))
	if err = flow.Adopt(tx3); err != nil {
		panic(err)
	}

	b, stage, receipts, err := flow.Pack(genesis.DevAccounts()[0].PrivateKey, block.MBlockType, 0)
	if _, err := stage.Commit(); err != nil {
		panic(err)
	}
	b.SetQC(&block.QuorumCert{QCHeight: 1, QCRound: 1, EpochID: 1, VoterBitArrayStr: "X_XXX", VoterMsgHash: meter.BytesToBytes32([]byte("hello")), VoterAggSig: []byte("voteraggr")})
	escortQC := &block.QuorumCert{QCHeight: b.Number(), QCRound: b.QC.QCRound + 1, EpochID: b.QC.EpochID, VoterMsgHash: b.VotingHash()}
	if _, err = c.AddBlock(b, escortQC, receipts); err != nil {
		panic(err)
	}
	st, _ := state.New(b.Header().StateRoot(), kv)

	sdb := statedb.New(st)

	// Execute a bucketUpdate(sub) for Voter2 bucket
	rtp := runtime.New(seeker, st,
		&xenv.BlockContext{Time: currentTs,
			Number: meter.TeslaFork9_MainnetStartNum + 1,
			Signer: tests.HolderAddr})
	bktID := tests.BucketID(tests.Voter2Addr, 0, 0)
	body := &staking.StakingBody{
		Opcode:     staking.OP_BUCKET_UPDT,
		Version:    0,
		Option:     meter.BUCKET_SUB_OPT,
		Amount:     tests.BuildAmount(100),
		HolderAddr: tests.Voter2Addr,
		StakingID:  bktID,
		Token:      meter.MTRG,
		Timestamp:  uint64(0),
		Nonce:      0,
	}
	txNonce := rand.Uint64()
	subtx := tests.BuildStakingTx(c.Tag(), 0, body, tests.Voter2Key, txNonce)

	if _, err := rtp.ExecuteTransaction(subtx); err != nil {
		panic(err)
	}
	for _, b := range st.GetBucketList().Buckets {
		fmt.Println("bucket: ", b.ToString())
	}

	rt := runtime.New(seeker, st,
		&xenv.BlockContext{Time: currentTs + 7*24*3600 + 721,
			Number: meter.TeslaFork9_MainnetStartNum + 1,
			Signer: tests.HolderAddr})

	rt.EnforceTeslaFork9_Corrections(sdb, big.NewInt(0))
	fmt.Println("CURRENT X: ", currentTs+7*24*3600+721)
	return &tests.TestEnv{Runtime: rt, State: st, BktCreateTS: 0, CurrentTS: currentTs + 7*24*3600 + 721, ChainTag: c.Tag()}
}
