package consensus

import (
	"log/slog"
	"sync"
	"time"

	"github.com/antexprotocol/supernova/chain"
	"github.com/antexprotocol/supernova/types"
)

// ForkDetector detect potential fork attacks
type ForkDetector struct {
	chain  *chain.Chain
	logger *slog.Logger

	mu sync.RWMutex

	// track observed different chains
	observedChains map[types.Bytes32]*ChainInfo

	// fork detection parameters
	maxForkAge    time.Duration
	forkThreshold int // fork threshold, how many different chain heads are considered a fork
}

// ChainInfo record observed chain information
type ChainInfo struct {
	HeadBlockID types.Bytes32
	Height      uint32
	QCRound     uint32
	FirstSeen   time.Time
	LastSeen    time.Time
	Count       int // number of times this chain head has been seen
}

// NewForkDetector create a new fork detector
func NewForkDetector(chain *chain.Chain) *ForkDetector {
	return &ForkDetector{
		chain:          chain,
		logger:         slog.With("pkg", "fork_detector"),
		observedChains: make(map[types.Bytes32]*ChainInfo),
		maxForkAge:     5 * time.Minute,
		forkThreshold:  2,
	}
}

// ObserveChain observe a chain head
func (fd *ForkDetector) ObserveChain(blockID types.Bytes32, height uint32, qcRound uint32) {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	now := time.Now()

	if info, exists := fd.observedChains[blockID]; exists {
		info.LastSeen = now
		info.Count++
	} else {
		fd.observedChains[blockID] = &ChainInfo{
			HeadBlockID: blockID,
			Height:      height,
			QCRound:     qcRound,
			FirstSeen:   now,
			LastSeen:    now,
			Count:       1,
		}
	}

	// clean up expired observations
	fd.cleanupOldObservations(now)
}

// DetectFork detect if there is a fork
func (fd *ForkDetector) DetectFork() *ForkInfo {
	fd.mu.RLock()
	defer fd.mu.RUnlock()

	now := time.Now()
	activeChains := make([]*ChainInfo, 0)

	// collect active chains
	for _, info := range fd.observedChains {
		if now.Sub(info.LastSeen) < fd.maxForkAge {
			activeChains = append(activeChains, info)
		}
	}

	// if there is only one active chain, no fork
	if len(activeChains) <= 1 {
		return nil
	}

	// analyze fork situation
	return fd.analyzeFork(activeChains)
}

// ForkInfo fork information
type ForkInfo struct {
	DetectedAt    time.Time
	ChainCount    int
	MyChainRank   int  // our rank in all chains (by height and support)
	IsUnderAttack bool // whether we are under attack
	ShouldSwitch  bool // whether we should switch to other chains
}

// analyzeFork analyze fork situation
func (fd *ForkDetector) analyzeFork(chains []*ChainInfo) *ForkInfo {
	myBlock := fd.chain.BestBlock()
	myBlockID := myBlock.ID()

	forkInfo := &ForkInfo{
		DetectedAt: time.Now(),
		ChainCount: len(chains),
	}

	// sort chains by height and support
	maxHeight := uint32(0)
	myRank := len(chains) // default lowest rank

	for i, info := range chains {
		if info.Height > maxHeight {
			maxHeight = info.Height
		}

		if info.HeadBlockID == myBlockID {
			myRank = i + 1
		}
	}

	forkInfo.MyChainRank = myRank

	// check if we are under attack
	myHeight := myBlock.Number()
	heightDiff := maxHeight - myHeight

	// if other chains are significantly higher, it might be an attack
	if heightDiff > 3 && myRank > 1 {
		forkInfo.IsUnderAttack = true

		// but don't switch immediately, wait for more information
		forkInfo.ShouldSwitch = false

		fd.logger.Warn("potential fork attack detected",
			"myHeight", myHeight,
			"maxHeight", maxHeight,
			"heightDiff", heightDiff,
			"myRank", myRank,
			"totalChains", len(chains))
	}

	return forkInfo
}

// cleanupOldObservations clean up expired observations
func (fd *ForkDetector) cleanupOldObservations(now time.Time) {
	for id, info := range fd.observedChains {
		if now.Sub(info.LastSeen) > fd.maxForkAge {
			delete(fd.observedChains, id)
		}
	}
}

// IsLikelyLegitimate check if current node is likely on a legitimate chain
func (fd *ForkDetector) IsLikelyLegitimate() bool {
	forkInfo := fd.DetectFork()

	// if no fork is detected, consider it legitimate
	if forkInfo == nil {
		return true
	}

	// if our rank is good enough, or uncertain if it's an attack, continue to consider it legitimate
	if forkInfo.MyChainRank <= 2 || !forkInfo.IsUnderAttack {
		return true
	}

	// only consider it illegitimate if we are clearly behind and an attack is detected
	return false
}
