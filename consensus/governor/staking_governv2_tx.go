package governor

import (
	"bytes"
	"log/slog"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/script"
	"github.com/meterio/meter-pov/script/staking"
	"github.com/meterio/meter-pov/tx"
)

// for distribute validator rewards, recalc the delegates list ...
func BuildStakingGoverningV2Tx(rewardInfoV2s []*meter.RewardInfoV2, curEpoch uint32, chainTag byte, bestNum uint32) *tx.Transaction {
	// 1. signer is nil
	// 2. in kblock.
	builder := new(tx.Builder)
	builder.ChainTag(chainTag).
		BlockRef(tx.NewBlockRef(bestNum)).
		Expiration(720).
		GasPriceCoef(0).
		Gas(meter.BaseTxGas * 10). //buffer for builder.Build().IntrinsicGas()
		DependsOn(nil).
		Nonce(12345678)

	builder.Clause(
		tx.NewClause(&meter.StakingModuleAddr).
			WithValue(big.NewInt(0)).
			WithToken(meter.MTRG).
			WithData(buildStakingGoverningV2Data(rewardInfoV2s, curEpoch)))
	gas, _ := builder.Build().IntrinsicGas()
	builder.Gas(gas)
	return builder.Build()
}

func buildStakingGoverningV2Data(rewardInfoV2s []*meter.RewardInfoV2, curEpoch uint32) (ret []byte) {
	totalRewards := big.NewInt(0)
	for _, rinfo := range rewardInfoV2s {
		totalRewards.Add(totalRewards, rinfo.DistAmount)
		totalRewards.Add(totalRewards, rinfo.AutobidAmount)
	}
	sort.SliceStable(rewardInfoV2s, func(i, j int) bool {
		return bytes.Compare(rewardInfoV2s[i].Address[:], rewardInfoV2s[j].Address[:]) <= 0
	})

	// 52 bytes for each rewardInfo, Tx can accommodate about 1000 rewardinfo
	extraBytes, err := rlp.EncodeToBytes(rewardInfoV2s)
	if err != nil {
		slog.Info("encode validators failed", "error", err.Error())
		return
	}

	body := &staking.StakingBody{
		Opcode:    staking.OP_GOVERNING,
		Version:   curEpoch,
		Option:    uint32(0),
		Amount:    totalRewards,
		Timestamp: 0, //uint64(time.Now().Unix()),
		Nonce:     0, //rand.Uint64(),
		ExtraData: extraBytes,
	}
	ret, _ = script.EncodeScriptData(body)
	return
}
