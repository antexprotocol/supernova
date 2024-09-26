package staking

import (
	"errors"

	"github.com/meterio/meter-pov/meter"
	setypes "github.com/meterio/meter-pov/script/types"
)

func (s *Staking) DelegateHandler(env *setypes.ScriptEnv, sb *StakingBody, gas uint64) (leftOverGas uint64, err error) {
	var ret []byte
	defer func() {
		if err != nil {
			ret = []byte(err.Error())
		}
		env.SetReturnData(ret)
	}()
	state := env.GetState()
	candidateList := state.GetCandidateList()
	bucketList := state.GetBucketList()
	stakeholderList := state.GetStakeHolderList()

	if gas < meter.ClauseGas {
		leftOverGas = 0
	} else {
		leftOverGas = gas - meter.ClauseGas
	}

	b := bucketList.Get(sb.StakingID)
	if b == nil {
		return leftOverGas, errBucketNotFound
	}
	if b.Owner != sb.HolderAddr {
		return leftOverGas, errBucketOwnerMismatch
	}
	if b.Value.Cmp(sb.Amount) != 0 {
		return leftOverGas, errBucketAmountMismatch
	}
	if b.Token != sb.Token {
		return leftOverGas, errBucketTokenMismatch
	}
	if b.IsForeverLock() {
		return leftOverGas, errUpdateForeverBucket
	}
	if !b.Candidate.IsZero() {
		s.logger.Error("bucket is in use", "candidate", b.Candidate)
		return leftOverGas, errBucketInUse
	}

	if sb.Autobid > 100 {
		err = errors.New("autobid > 100%")
		s.logger.Error("errors", "error", err)
		return
	}

	cand := candidateList.Get(sb.CandAddr)
	if cand == nil {
		return leftOverGas, errBucketNotFound
	}

	number := env.GetBlockNum()
	selfRatioValid := false
	if meter.IsTeslaFork8(number) {
		selfRatioValid = CorrectCheckEnoughSelfVotes(cand, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, b.Value, nil)
	} else if meter.IsTeslaFork1(number) {
		selfRatioValid = CheckCandEnoughSelfVotes(sb.Amount, cand, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO)
	} else {
		selfRatioValid = CheckCandEnoughSelfVotes(b.TotalVotes, cand, bucketList, meter.TESLA1_0_SELF_VOTE_RATIO)
	}
	if !selfRatioValid {
		s.logger.Error(errCandidateNotEnoughSelfVotes.Error(), "candidate", cand.Addr.String())
		return leftOverGas, errCandidateNotEnoughSelfVotes
	}

	// emit NativeBucketUpdateCandidate
	if meter.IsTeslaFork10(number) {
		env.AddNativeBucketUpdateCandidate(b.Owner, b.BucketID, b.Candidate, sb.CandAddr)
	}

	// sanity check done, take actions
	b.Candidate = sb.CandAddr
	b.Autobid = sb.Autobid
	cand.AddBucket(b)

	state.SetCandidateList(candidateList)
	state.SetBucketList(bucketList)
	state.SetStakeHolderList(stakeholderList)
	return
}
