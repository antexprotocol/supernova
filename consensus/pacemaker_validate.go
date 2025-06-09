package consensus

import (
	sha256 "crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/antexprotocol/supernova/block"
)

func (p *Pacemaker) ValidateQC(b *block.Block, escortQC *block.QuorumCert) bool {
	var valid bool
	var err error

	h := sha256.New()
	h.Write(escortQC.ToBytes())
	qcID := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if validQCs.Contains(qcID) {
		return true
	}
	// validate with current committee
	if p.epochState.CommitteeSize() <= 0 {
		fmt.Println("verify QC with empty p.committee")
		return false
	}
	valid, err = b.VerifyQC(escortQC, p.blsMaster, p.epochState.committee)
	if valid && err == nil {
		p.logger.Debug(fmt.Sprintf("validated %s", escortQC.CompactString()))
		validQCs.Add(qcID, true)
		return true
	}
	p.logger.Warn(fmt.Sprintf("validate %s FAILED", escortQC.CompactString()), "err", err, "committeeSize", p.epochState.CommitteeSize())
	return false
}

// validateReceivedQC strictly validate received QC, prevent malicious QC attack
// 🔥 critical security mechanism: do not trust any claimed "higher QC"
func (p *Pacemaker) validateReceivedQC(qc *block.QuorumCert) bool {
	if qc == nil {
		return false
	}

	// check if QC's epoch matches
	if qc.Epoch != p.epochState.epoch {
		p.logger.Warn("QC epoch mismatch", "qcEpoch", qc.Epoch, "myEpoch", p.epochState.epoch)
		return false
	}

	// get block pointed by QC
	qcBlock, err := p.chain.GetBlock(qc.BlockID)
	if qcBlock == nil || err != nil {
		// if we don't have this block, cannot validate QC's validity
		// but also should not blindly accept
		p.logger.Debug("QC refers to unknown block", "blockID", qc.BlockID.ToBlockShortID(), "err", err)
		return false
	}

	// strictly validate QC signature
	valid := p.ValidateQC(qcBlock, qc)
	if !valid {
		p.logger.Warn("QC signature validation failed", "qc", qc.CompactString())
		return false
	}

	// extra check: QC's round cannot be too far in future (prevent malicious prediction of future rounds)
	if qc.Round > p.currentRound+10 { // allow some network delay tolerance
		p.logger.Warn("QC round too far in future", "qcRound", qc.Round, "currentRound", p.currentRound)
		return false
	}

	p.logger.Debug("QC validation passed", "qc", qc.CompactString())
	return true
}
