package metertracker

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log/slog"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/meterio/meter-pov/meter"
)

var (
	errLessThanMinBoundBalance         = errors.New("bound amount < minimal " + new(big.Int).Div(meter.MIN_BOUND_BALANCE, big.NewInt(1e18)).String() + " MTRG")
	errLessThanMinCandidateRequirement = errors.New("bound amount < minimal candidate require")
	errZeroAmount                      = errors.New("zero amount")
	errEmptyCandidate                  = errors.New("empty candidate address")
	errCandidateNotListed              = errors.New("candidate not listed")
	errNotEnoughBalance                = errors.New("not enough balance")
	errNotEnoughBoundedBalance         = errors.New("not enough bounded balance")
	errSelfVoteNotAllowed              = errors.New("self vote not allowed")
	errNotEnoughVotes                  = errors.New("not enough votes")
	errCandidateNotEnoughSelfVotes     = errors.New("candidate's accumulated votes > 100x candidate's own vote")
	errCandidateJailed                 = errors.New("candidate is jailed")

	errInvalidAutobid                 = errors.New("invalid autobid")
	errInvalidCommission              = errors.New("invalid commission")
	errInvalidPubkey                  = errors.New("invalid pubkey")
	errCandidateInJail                = errors.New("candidate in jail")
	errInvalidIP                      = errors.New("invalid IP")
	errInvalidPort                    = errors.New("invalid port")
	errPubkeyListed                   = errors.New("pubkey listed")
	errNameListed                     = errors.New("name listed")
	errIPListed                       = errors.New("ip listed")
	errCandidateListed                = errors.New("candidate listed")
	errCandidateListedWithDiffInfo    = errors.New("candidate listed with diff info")
	errBucketNotListed                = errors.New("bucket not listed")
	errBucketNotOwned                 = errors.New("bucket not owned")
	errNoUpdateAllowedOnForeverBucket = errors.New("no update allowed on forever bucket")
	errBucketAlreadyUnbounded         = errors.New("bucket already unbounded")
	errBucketNotEnoughValue           = errors.New("not enough value")
	errBucketNotMergableToItself      = errors.New("bucket not mergable to itself")
	errUpdateTooFrequent              = errors.New("candidate update too frequent")
	errCandidateNotChanged            = errors.New("candidate not changed")
)

func (e *MeterTracker) BoundMeterGov(addr meter.Address, amount *big.Int) error {
	if amount.Sign() == 0 {
		return nil
	}
	state := e.state
	meterGov := state.GetBalance(addr)
	meterGovBounded := state.GetBoundedBalance(addr)

	// meterGov should >= amount
	if meterGov.Cmp(amount) == -1 {
		return errNotEnoughBalance
	}

	state.SetBalance(addr, new(big.Int).Sub(meterGov, amount))
	state.SetBoundedBalance(addr, new(big.Int).Add(meterGovBounded, amount))

	return nil
}

func (e *MeterTracker) UnboundMeterGov(addr meter.Address, amount *big.Int) error {
	if amount.Sign() == 0 {
		return nil
	}
	state := e.state
	meterGov := state.GetBalance(addr)
	meterGovBounded := state.GetBoundedBalance(addr)

	// meterGov should >= amount
	if meterGov.Cmp(amount) == -1 {
		return errNotEnoughBalance
	}

	state.SetBalance(addr, new(big.Int).Sub(meterGov, amount))
	state.SetBoundedBalance(addr, new(big.Int).Add(meterGovBounded, amount))

	return nil
}

// create a bucket
func (e *MeterTracker) BucketOpen(owner meter.Address, candAddr meter.Address, amount *big.Int, ts uint64, nonce uint64) (bucketID meter.Bytes32, err error) {
	emptyBucketID := meter.Bytes32{}
	injailList := e.state.GetInJailList()

	// assert amount not 0
	if amount.Sign() == 0 {
		return emptyBucketID, errZeroAmount
	}

	// assert amount should meet the stake minmial requirement
	if amount.Cmp(meter.MIN_BOUND_BALANCE) < 0 {
		return emptyBucketID, errLessThanMinBoundBalance
	}

	// assert candidate not empty
	if candAddr.IsZero() {
		return emptyBucketID, errEmptyCandidate
	}

	// assert balance(owner) > amount
	if e.state.GetBalance(owner).Cmp(amount) < 0 {
		return emptyBucketID, errNotEnoughBalance
	}

	// assert not self vote
	if owner.EqualFold(&candAddr) {
		return emptyBucketID, errSelfVoteNotAllowed
	}

	// assert candidate not in jail
	jailed := injailList.Get(candAddr)
	if jailed != nil {
		return emptyBucketID, errCandidateJailed
	}

	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()

	// for existing bucket, convert this request into a bucket deposit
	for _, bkt := range bucketList.Buckets {
		if candAddr.EqualFold(&bkt.Candidate) && owner.EqualFold(&bkt.Owner) {
			return bkt.ID(), e.BucketDeposit(owner, bkt.ID(), amount)
		}
	}

	candidate := candidateList.Get(candAddr)
	if candidate == nil {
		return emptyBucketID, errCandidateNotListed
	}

	if selfRatioValid := CorrectCheckEnoughSelfVotes(candidate, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, amount, nil); !selfRatioValid {
		return emptyBucketID, errCandidateNotEnoughSelfVotes
	}

	meterGov := e.state.GetBalance(owner)
	meterGovBounded := e.state.GetBoundedBalance(owner)

	e.state.SetBalance(owner, new(big.Int).Sub(meterGov, amount))
	e.state.SetBoundedBalance(owner, new(big.Int).Add(meterGovBounded, amount))

	newBucket := meter.NewBucket(owner, candAddr, amount, meter.MTRG, meter.ONE_WEEK_LOCK, meter.ONE_WEEK_LOCK_RATE, 100 /*autobid*/, ts, nonce)
	bucketList.Add(newBucket)
	candidate.AddBucket(newBucket)

	e.state.SetCandidateList(candidateList)
	e.state.SetBucketList(bucketList)

	return newBucket.ID(), nil
}

func (e *MeterTracker) BucketClose(owner meter.Address, id meter.Bytes32, timestamp uint64) error {
	bucketList := e.state.GetBucketList()
	b := bucketList.Get(id)
	if err := e.checkBucket(b, owner); err != nil {
		return err
	}

	// sanity check done, take action
	b.Unbounded = true
	b.MatureTime = timestamp + meter.GetBoundLocktime(b.Option) // lock time

	e.state.SetBucketList(bucketList)
	return nil
}

func (e *MeterTracker) checkBucket(b *meter.Bucket, owner meter.Address) error {
	// assert bucket listed
	if b == nil {
		return errBucketNotListed
	}

	// assert bucket not unbounded
	if b.Unbounded {
		return errBucketAlreadyUnbounded
	}

	// assert bucket owned
	if b.Owner != owner {
		return errBucketNotOwned
	}

	// assert bucket not forever
	if b.IsForeverLock() {
		return errNoUpdateAllowedOnForeverBucket
	}
	return nil
}

func CorrectCheckEnoughSelfVotes(c *meter.Candidate, bl *meter.BucketList, selfVoteRatio int64, addSelfValue *big.Int, subSelfValue *big.Int, addTotalValue *big.Int, subTotalValue *big.Int) bool {
	selfValue := big.NewInt(0)
	totalValue := big.NewInt(0)
	for _, b := range bl.Buckets {
		if b.Owner == c.Addr && b.Candidate == c.Addr && b.IsForeverLock() {
			// self candidate bucket
			selfValue.Add(selfValue, b.Value)
		}
		if b.Candidate == c.Addr {
			totalValue.Add(totalValue, b.Value)
		}
	}
	if addSelfValue != nil {
		selfValue.Add(selfValue, addSelfValue)
	}
	if subSelfValue != nil {
		if selfValue.Cmp(subSelfValue) > 0 {
			selfValue.Sub(selfValue, subSelfValue)
		} else {
			selfValue = new(big.Int)
		}
	}

	if addTotalValue != nil {
		totalValue.Add(totalValue, addTotalValue)
	}
	if subTotalValue != nil {
		if totalValue.Cmp(subTotalValue) > 0 {
			totalValue.Sub(totalValue, subTotalValue)
		} else {
			totalValue = new(big.Int)
		}
	}

	// fmt.Println("---------- CHECK SELF VOTE RATIO ----------")
	// fmt.Println("Candidate: ", c.Addr)
	// fmt.Println("Total Votes: ", c.TotalVotes, ", totalValue: ", totalValue.String())
	// fmt.Println("selfValue: ", selfValue.String())
	// fmt.Println("selfVoteRatio: ", selfVoteRatio)

	// enforce: candidate total votes / self votes <= selfVoteRatio
	// that means total votes / selfVoteRatio <= self votes
	limitSelfTotalValue := new(big.Int).Div(totalValue, big.NewInt(selfVoteRatio))

	result := limitSelfTotalValue.Cmp(selfValue) <= 0
	slog.Info("check self vote ratio", "candidate", c.Addr, "totalVotes", totalValue.String(), "selfVotes", selfValue.String(), "selfVoteRatio", selfVoteRatio, "result", result)
	// fmt.Println("Result: ", result)
	// fmt.Println("-------------------------------------------")
	return result

}

func (e *MeterTracker) BucketDeposit(owner meter.Address, id meter.Bytes32, amount *big.Int) error {
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()
	injailList := e.state.GetInJailList()

	b := bucketList.Get(id)
	if err := e.checkBucket(b, owner); err != nil {
		return err
	}

	// assert balance(owner) > amount
	if e.state.GetBalance(owner).Cmp(amount) < 0 {
		return errNotEnoughBalance
	}

	cand := candidateList.Get(b.Candidate)

	// assert candidate has valid self vote ratio
	if cand != nil {
		if selfRatioValid := CorrectCheckEnoughSelfVotes(cand, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, amount, nil); !selfRatioValid {
			return errCandidateNotEnoughSelfVotes
		}
	}

	// assert candidate not in jail
	jailed := injailList.Get(b.Candidate)
	if jailed != nil {
		return errCandidateJailed
	}

	// bound account balance
	err := e.BoundMeterGov(owner, amount)
	if err != nil {
		return err
	}

	// update bucket values
	b.BonusVotes = 0
	b.Value.Add(b.Value, amount)
	b.TotalVotes.Add(b.TotalVotes, amount)

	// update candidate totalVotes with deposited amount
	if !b.Candidate.IsZero() {
		if cand := candidateList.Get(b.Candidate); cand != nil {
			cand.TotalVotes.Add(cand.TotalVotes, amount)
		}
	}

	e.state.SetBucketList(bucketList)
	e.state.SetCandidateList(candidateList)
	return nil
}

func (e *MeterTracker) BucketWithdraw(owner meter.Address, id meter.Bytes32, amount *big.Int, recipient meter.Address, ts uint64, nonce uint64) (meter.Bytes32, error) {
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()

	emptyBktID := meter.Bytes32{}
	b := bucketList.Get(id)

	// basic check for bucket
	if err := e.checkBucket(b, owner); err != nil {
		return emptyBktID, err
	}

	// assert boundedBalance(owner) > amount
	if e.state.GetBoundedBalance(owner).Cmp(amount) < 0 {
		return emptyBktID, errNotEnoughBoundedBalance
	}

	// assert bucket value > amount
	if b.Value.Cmp(amount) < 0 || b.TotalVotes.Cmp(amount) < 0 {
		return emptyBktID, errBucketNotEnoughValue
	}

	// assert leftover votes > staking requirement
	valueAfterWithdraw := new(big.Int).Sub(b.Value, amount)
	if valueAfterWithdraw.Cmp(meter.MIN_BOUND_BALANCE) < 0 {
		return emptyBktID, errLessThanMinBoundBalance
	}

	// bonus is substracted porpotionally
	oldBonus := new(big.Int).Sub(b.TotalVotes, b.Value)
	// bonus delta = oldBonus * (amount/bucket value)
	bonusDelta := new(big.Int).Mul(oldBonus, amount)
	bonusDelta.Div(bonusDelta, b.Value)

	// update old bucket
	b.BonusVotes = 0
	b.Value.Sub(b.Value, amount)
	b.TotalVotes.Sub(b.TotalVotes, amount)
	b.TotalVotes.Sub(b.TotalVotes, bonusDelta)

	// transfer bounded balance
	ownerBounded := e.state.GetBoundedBalance(owner)
	e.state.SetBoundedBalance(owner, new(big.Int).Sub(ownerBounded, amount))
	recipientBounded := e.state.GetBoundedBalance(recipient)
	e.state.SetBoundedBalance(recipient, new(big.Int).Add(recipientBounded, amount))

	// create unbounded new bucket
	newBucket := meter.NewBucket(recipient, b.Candidate, amount, uint8(b.Token), meter.ONE_WEEK_LOCK, b.Rate, b.Autobid, ts, nonce)
	newBucket.Unbounded = true
	newBucket.MatureTime = ts + meter.GetBoundLocktime(newBucket.Option) // lock time
	newBucketID := newBucket.BucketID

	cand := candidateList.Get(b.Candidate)
	if cand != nil {
		cand.TotalVotes.Sub(cand.TotalVotes, bonusDelta)
		cand.Buckets = append(cand.Buckets, newBucketID)
	}
	// update bucket list with new bucket
	bucketList.Add(newBucket)

	e.state.SetBucketList(bucketList)
	e.state.SetCandidateList(candidateList)
	return newBucketID, nil
}

func (e *MeterTracker) BucketUpdateCandidate(owner meter.Address, id meter.Bytes32, newCandidateAddr meter.Address) error {
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()
	injailList := e.state.GetInJailList()

	b := bucketList.Get(id)
	// assert bucket listed
	if b == nil {
		return errBucketNotListed
	}

	// assert bucket owned
	if b.Owner != owner {
		return errBucketNotOwned
	}

	// assert bucket not forever
	if b.IsForeverLock() {
		return errNoUpdateAllowedOnForeverBucket
	}

	// assert candidate listed
	nc := candidateList.Get(newCandidateAddr)
	if nc == nil {
		return errCandidateNotListed
	}

	// assert candidate not in jail
	jailed := injailList.Get(newCandidateAddr)
	if jailed != nil {
		return errCandidateJailed
	}

	// assert new candidate has valid self vote ratio
	if selfRatioValid := CorrectCheckEnoughSelfVotes(nc, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, b.TotalVotes, nil); !selfRatioValid {
		return errCandidateNotEnoughSelfVotes
	}

	c := candidateList.Get(b.Candidate)
	// subtract totalVotes from old candidate
	if c != nil {
		if c.TotalVotes.Cmp(b.TotalVotes) < 0 {
			return errNotEnoughVotes
		}
		// c.TotalVotes.Sub(c.TotalVotes, b.TotalVotes)
		c.RemoveBucket(b)
	}
	// add totalVotes to new candidate
	// nc.TotalVotes.Add(nc.TotalVotes, b.TotalVotes)
	nc.AddBucket(b)
	b.Candidate = nc.Addr

	e.state.SetBucketList(bucketList)
	e.state.SetCandidateList(candidateList)
	return nil
}

func (e *MeterTracker) BucketMerge(owner meter.Address, fromBucketID meter.Bytes32, toBucketID meter.Bytes32) error {
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()
	injailList := e.state.GetInJailList()

	// assert from/to is not the same
	if strings.EqualFold(fromBucketID.String(), toBucketID.String()) {
		return errBucketNotMergableToItself
	}

	fromBkt := bucketList.Get(fromBucketID)
	toBkt := bucketList.Get(toBucketID)

	// basic check for from bucket
	if err := e.checkBucket(fromBkt, owner); err != nil {
		return err
	}
	// basic check for to bucket
	if err := e.checkBucket(toBkt, owner); err != nil {
		return err
	}

	fromCand := candidateList.Get(fromBkt.Candidate)
	toCand := candidateList.Get(toBkt.Candidate)

	// assert candidate not in jail
	jailed := injailList.Get(toBkt.Candidate)
	if jailed != nil {
		return errCandidateJailed
	}

	// assert to candidate has valid self vote ratio
	if toCand != nil {
		if selfRatioValid := CorrectCheckEnoughSelfVotes(toCand, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, fromBkt.TotalVotes, nil); !selfRatioValid {
			return errCandidateNotEnoughSelfVotes
		}
	}

	if fromCand != nil {
		fromCand.RemoveBucket(fromBkt)
	}
	// BonusVotes has been deprecated, could be inferred by (totalVotes - value)
	toBkt.BonusVotes = toBkt.BonusVotes + fromBkt.BonusVotes
	toBkt.Value.Add(toBkt.Value, fromBkt.Value)
	toBkt.TotalVotes.Add(toBkt.TotalVotes, fromBkt.TotalVotes)

	if toCand != nil {
		toCand.TotalVotes.Add(toCand.TotalVotes, fromBkt.TotalVotes)
	}

	bucketList.Remove(fromBucketID)
	e.state.SetBucketList(bucketList)
	e.state.SetCandidateList(candidateList)
	return nil
}

func (e *MeterTracker) BucketTransferFund(owner meter.Address, fromBucketID meter.Bytes32, toBucketID meter.Bytes32, amount *big.Int) error {
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()
	injailList := e.state.GetInJailList()

	fromBkt := bucketList.Get(fromBucketID)
	toBkt := bucketList.Get(toBucketID)

	// assert candidate not in jail
	jailed := injailList.Get(toBkt.Candidate)
	if jailed != nil {
		return errCandidateJailed
	}

	// assert from/to is not the same
	if strings.EqualFold(fromBucketID.String(), toBucketID.String()) {
		return errBucketNotMergableToItself
	}

	// basic check for from bucket
	if err := e.checkBucket(fromBkt, owner); err != nil {
		return err
	}
	// basic check for to bucket
	if err := e.checkBucket(toBkt, owner); err != nil {
		return err
	}

	fromCand := candidateList.Get(fromBkt.Candidate)
	toCand := candidateList.Get(toBkt.Candidate)

	// assert to candidate has valid self vote ratio
	if toCand != nil {
		if selfRatioValid := CorrectCheckEnoughSelfVotes(toCand, bucketList, meter.TESLA1_1_SELF_VOTE_RATIO, nil, nil, amount, nil); !selfRatioValid {
			return errCandidateNotEnoughSelfVotes
		}
	}

	// assert boundedBalance(owner) > amount
	if e.state.GetBoundedBalance(owner).Cmp(amount) < 0 {
		return errNotEnoughBoundedBalance
	}

	// assert from bucket value > amount
	if fromBkt.Value.Cmp(amount) < 0 || fromBkt.TotalVotes.Cmp(amount) < 0 {
		return errBucketNotEnoughValue
	}

	// assert leftover votes > staking requirement
	valueAfterTransfer := new(big.Int).Sub(fromBkt.Value, amount)
	if valueAfterTransfer.Cmp(meter.MIN_BOUND_BALANCE) < 0 {
		return errLessThanMinBoundBalance
	}

	// bonus is substracted porpotionally
	fromBonus := new(big.Int).Sub(fromBkt.TotalVotes, fromBkt.Value)
	// bonus delta = oldBonus * (amount/bucket value)
	bonusDelta := new(big.Int).Mul(fromBonus, amount)
	bonusDelta.Div(bonusDelta, fromBkt.Value)

	// update from bucket
	fromBkt.BonusVotes = new(big.Int).Sub(fromBonus, bonusDelta).Uint64()
	fromBkt.Value.Sub(fromBkt.Value, amount)
	fromBkt.TotalVotes.Sub(fromBkt.TotalVotes, amount)
	fromBkt.TotalVotes.Sub(fromBkt.TotalVotes, bonusDelta)

	// update to bucket
	toBkt.BonusVotes = toBkt.BonusVotes + bonusDelta.Uint64()
	toBkt.Value.Add(toBkt.Value, amount)
	toBkt.TotalVotes.Add(toBkt.TotalVotes, amount)
	toBkt.TotalVotes.Add(toBkt.TotalVotes, bonusDelta)

	// update from candidate if exists
	if fromCand != nil {
		fromCand.TotalVotes.Sub(fromCand.TotalVotes, amount)
		fromCand.TotalVotes.Sub(fromCand.TotalVotes, bonusDelta)
	}

	// update to candidate if exists
	if toCand != nil {
		toCand.TotalVotes.Add(toCand.TotalVotes, amount)
		toCand.TotalVotes.Add(toCand.TotalVotes, bonusDelta)
	}

	e.state.SetBucketList(bucketList)
	e.state.SetCandidateList(candidateList)
	return nil
}

func (e *MeterTracker) BucketValue(id meter.Bytes32) (*big.Int, error) {
	bucketList := e.state.GetBucketList()
	b := bucketList.Get(id)
	if b == nil {
		return new(big.Int), errBucketNotListed
	}
	return b.Value, nil
}

func (e *MeterTracker) validatePubKey(comboPubKey []byte) ([]byte, error) {
	pubKey := strings.TrimSuffix(string(comboPubKey), "\n")
	pubKey = strings.TrimSuffix(pubKey, " ")
	split := strings.Split(pubKey, ":::")
	if len(split) != 2 {
		e.logger.Error("invalid public keys for split")
		return nil, errInvalidPubkey
	}

	// validate ECDSA pubkey
	decoded, err := base64.StdEncoding.DecodeString(split[0])
	if err != nil {
		e.logger.Error("could not decode ECDSA public key")
		return nil, errInvalidPubkey
	}
	_, err = crypto.UnmarshalPubkey(decoded)
	if err != nil {
		e.logger.Error("could not unmarshal ECDSA public key")
		return nil, errInvalidPubkey
	}

	// validate BLS key
	_, err = base64.StdEncoding.DecodeString(split[1])
	if err != nil {
		e.logger.Error("could not decode BLS public key")
		return nil, errInvalidPubkey
	}
	// TODO: validate BLS key with bls common

	return []byte(pubKey), nil
}

func (e *MeterTracker) ListAsCandidate(owner meter.Address, name []byte, description []byte, pubkey []byte, ip []byte, port uint16, amount *big.Int, autobid uint8, commissionRate uint32, timestamp uint64, nonce uint64) (meter.Bytes32, error) {
	emptyBucketID := meter.Bytes32{}
	candidateList := e.state.GetCandidateList()
	bucketList := e.state.GetBucketList()
	stakeholderList := e.state.GetStakeHolderList()

	// candidate should meet the stake minmial requirement
	// current it is 300 MTRGov
	if amount.Cmp(meter.MIN_REQUIRED_BY_DELEGATE) < 0 {
		return emptyBucketID, errLessThanMinCandidateRequirement
	}

	if autobid > 100 {
		return emptyBucketID, errInvalidAutobid
	}

	candidatePubKey, err := e.validatePubKey(pubkey)
	if err != nil {
		return emptyBucketID, err
	}

	if port < 1 || port > 65535 {
		return emptyBucketID, errInvalidPort
	}

	ipPattern, _ := regexp.Compile("^\\d+[.]\\d+[.]\\d+[.]\\d+$")
	if !ipPattern.MatchString(string(ip)) {
		return emptyBucketID, errInvalidIP
	}

	for _, record := range candidateList.Candidates {
		pkListed := bytes.Equal(record.PubKey, []byte(candidatePubKey))
		ipListed := bytes.Equal(record.IPAddr, ip)
		nameListed := bytes.Equal(record.Name, name)

		if pkListed {
			return emptyBucketID, errPubkeyListed
		}
		if ipListed {
			return emptyBucketID, errIPListed
		}
		if nameListed {
			return emptyBucketID, errNameListed
		}
	}

	// domainPattern, err := regexp.Compile("^([0-9a-zA-Z-_]+[.]*)+$")
	// if the candidate already exists return error without paying gas
	if record := candidateList.Get(owner); record != nil {
		if bytes.Equal(record.PubKey, []byte(pubkey)) && bytes.Equal(record.IPAddr, ip) && record.Port == port {
			// exact same candidate
			// s.logger.Info("Record: ", record.ToString())
			// s.logger.Info("sb:", sb.ToString())
			return emptyBucketID, errCandidateListed
		} else {
			return emptyBucketID, errCandidateListedWithDiffInfo
		}
	}

	// now staking the amount, force to forever lock
	opt, rate, locktime := meter.GetBoundLockOption(meter.FOREVER_LOCK)
	commission := meter.GetCommissionRate(commissionRate)
	e.logger.Debug("get lock option in candidate", "option", opt, "rate", rate, "locktime", locktime, "commission", commission)

	// bucket owner is candidate
	// ts = env.GetBlockCtx().Time
	// nonce = env.GetTxCtx().Nonce + uint64(env.GetClauseIndex()) + env.GetTxCtx().Counter

	bucket := meter.NewBucket(owner, owner, amount, uint8(meter.MTRG), opt, rate, autobid, uint64(timestamp), nonce)
	bucketList.Add(bucket)

	candidate := meter.NewCandidate(owner, name, description, []byte(candidatePubKey), ip, port, commission, timestamp)
	candidate.AddBucket(bucket)
	candidateList.Add(candidate)

	stakeholder := stakeholderList.Get(owner)
	if stakeholder == nil {
		stakeholder = meter.NewStakeholder(owner)
		stakeholder.AddBucket(bucket)
		stakeholderList.Add(stakeholder)
	} else {
		stakeholder.AddBucket(bucket)
	}

	err = e.BoundMeterGov(owner, amount)
	if err != nil {
		return emptyBucketID, err
	}

	e.state.SetCandidateList(candidateList)
	e.state.SetBucketList(bucketList)
	e.state.SetStakeHolderList(stakeholderList)

	return bucket.BucketID, nil
}
