package fork7

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"math/rand"
	"testing"

	"github.com/meterio/meter-pov/consensus/governor"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/script/staking"
	"github.com/meterio/meter-pov/tests"
	"github.com/stretchr/testify/assert"
)

func TestReleaseMaturedBucket(t *testing.T) {
	rt, s, _ := initRuntimeAfterFork7()
	tx := governor.BuildStakingGoverningV2Tx([]*meter.RewardInfoV2{}, 1, byte(82), 0)
	bal := s.GetBalance(tests.HolderAddr)
	bktCount := s.GetBucketList().Len()
	receipt, err := rt.ExecuteTransaction(tx)
	assert.Nil(t, err)
	assert.False(t, receipt.Reverted)
	unboundTopic, _ := hex.DecodeString("745b53e5ab1a6d7d25f17a8ed30cbd14d6706acb1b397c7766de275d7c9ba232")
	unboundEvtCount := 0
	for _, o := range receipt.Outputs {
		for _, e := range o.Events {
			if len(e.Topics) > 0 && bytes.Equal(e.Topics[0][:], unboundTopic) {
				unboundEvtCount++
			}
		}
	}
	assert.Equal(t, 1, unboundEvtCount, "only 1 Unbound event")

	bucketList := s.GetBucketList()

	assert.Equal(t, bktCount-1, len(bucketList.Buckets), "bucket not released")
	balAfter := s.GetBalance(tests.HolderAddr)
	assert.Equal(t, new(big.Int).Sub(balAfter, bal).String(), "1000000000000000000000", "boundbalance not released to balance")
}

func TestDuplicateUnbound(t *testing.T) {
	rt, s, _ := initRuntimeAfterFork7()
	bucketList := s.GetBucketList()
	var bkt *meter.Bucket
	for _, b := range bucketList.Buckets {
		if b.Unbounded {
			bkt = b
			break
		}
	}
	body := &staking.StakingBody{
		Opcode:     staking.OP_UNBOUND,
		Version:    0,
		HolderAddr: tests.HolderAddr,
		Option:     uint32(0),
		Amount:     bkt.Value,
		StakingID:  bkt.ID(),
		Token:      meter.MTRG,
		Timestamp:  uint64(0),
		Nonce:      0,
	}
	txNonce := rand.Uint64()
	trx := tests.BuildStakingTx(82, 0, body, tests.HolderKey, txNonce)
	receipt, err := rt.ExecuteTransaction(trx)
	assert.Nil(t, err)
	assert.True(t, receipt.Reverted)
}
