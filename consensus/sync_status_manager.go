package consensus

import (
	"sync"
	"time"

	"log/slog"

	"github.com/antexprotocol/supernova/chain"
	"github.com/antexprotocol/supernova/types"
)

// SyncStatusManager manage node sync status
type SyncStatusManager struct {
	chain  *chain.Chain
	logger *slog.Logger

	mu            sync.RWMutex
	isSynced      bool
	lastSyncCheck time.Time
	syncStartTime time.Time

	// sync status configuration
	maxTimeLag        uint64 // maximum allowed time lag (nanoseconds)
	syncCheckInterval time.Duration
}

// NewSyncStatusManager create a new sync status manager
func NewSyncStatusManager(chain *chain.Chain) *SyncStatusManager {
	return &SyncStatusManager{
		chain:             chain,
		logger:            slog.With("pkg", "sync_status"),
		maxTimeLag:        types.BlockInterval * 3, // default 3 block intervals
		syncCheckInterval: 10 * time.Second,
		syncStartTime:     time.Now(),
	}
}

// IsSynced check if node is synced
func (ssm *SyncStatusManager) IsSynced() bool {
	ssm.mu.RLock()
	defer ssm.mu.RUnlock()

	// if not checked recently, check first
	if time.Since(ssm.lastSyncCheck) > ssm.syncCheckInterval {
		ssm.mu.RUnlock()
		ssm.checkSyncStatus()
		ssm.mu.RLock()
	}

	return ssm.isSynced
}

// checkSyncStatus check sync status (internal method)
func (ssm *SyncStatusManager) checkSyncStatus() {
	ssm.mu.Lock()
	defer ssm.mu.Unlock()

	oldStatus := ssm.isSynced
	ssm.lastSyncCheck = time.Now()

	bestBlock := ssm.chain.BestBlock()
	bestQC := ssm.chain.BestQC()
	nowNano := uint64(time.Now().UnixNano())

	// check1: time sync - but allow some time difference
	timeDiff := nowNano - bestBlock.NanoTimestamp()
	if bestBlock.NanoTimestamp() > nowNano {
		timeDiff = bestBlock.NanoTimestamp() - nowNano
	}

	// if time difference is too large, consider not synced (relaxed condition)
	if timeDiff > ssm.maxTimeLag*2 {
		ssm.isSynced = false
		ssm.logger.Debug("node not synced - severe time lag",
			"timeDiff", timeDiff,
			"maxAllowed", ssm.maxTimeLag*2)
		goto statusChanged
	}

	// check2: QC consistency - but not strictly require full match
	// in fork situations, QC may temporarily mismatch
	if bestQC.BlockID != bestBlock.ID() {
		// check if it's a short-term inconsistency (possibly normal fork handling)
		if timeDiff < types.BlockInterval*2 {
			// short-term inconsistency can be tolerated, possibly during fork recovery
			ssm.logger.Debug("temporary QC mismatch - may be resolving fork",
				"bestBlockID", bestBlock.ID().ToBlockShortID(),
				"bestQCBlockID", bestQC.BlockID.ToBlockShortID(),
				"timeDiff", timeDiff)
		} else {
			// if long-term inconsistency, consider not synced
			ssm.isSynced = false
			ssm.logger.Debug("node not synced - persistent QC mismatch",
				"bestBlockID", bestBlock.ID().ToBlockShortID(),
				"bestQCBlockID", bestQC.BlockID.ToBlockShortID(),
				"timeDiff", timeDiff)
			goto statusChanged
		}
	}

	// if all checks pass, consider synced
	ssm.isSynced = true

statusChanged:
	// if sync status changed, log it
	if oldStatus != ssm.isSynced {
		if ssm.isSynced {
			ssm.logger.Info("node synced",
				"timeSinceStart", time.Since(ssm.syncStartTime),
				"bestBlock", bestBlock.Number())
		} else {
			ssm.logger.Warn("node lost sync",
				"bestBlock", bestBlock.Number(),
				"timeDiff", timeDiff)
		}
	}
}

// MarkSyncStart mark sync start
func (ssm *SyncStatusManager) MarkSyncStart() {
	ssm.mu.Lock()
	defer ssm.mu.Unlock()

	ssm.isSynced = false
	ssm.syncStartTime = time.Now()
	ssm.logger.Info("sync started")
}

// ForceCheck force check sync status
func (ssm *SyncStatusManager) ForceCheck() bool {
	ssm.checkSyncStatus()
	return ssm.IsSynced()
}

// GetSyncInfo get sync information
func (ssm *SyncStatusManager) GetSyncInfo() (bool, time.Duration, time.Time) {
	ssm.mu.RLock()
	defer ssm.mu.RUnlock()

	return ssm.isSynced, time.Since(ssm.syncStartTime), ssm.lastSyncCheck
}

// SetMaxTimeLag set maximum time lag
func (ssm *SyncStatusManager) SetMaxTimeLag(lag uint64) {
	ssm.mu.Lock()
	defer ssm.mu.Unlock()
	ssm.maxTimeLag = lag
}

// IsSafeToPropose check if it is safe to propose
// this method is more lenient than IsSynced(), considering fork attack situations
func (ssm *SyncStatusManager) IsSafeToPropose() bool {
	ssm.mu.RLock()
	defer ssm.mu.RUnlock()

	bestBlock := ssm.chain.BestBlock()
	nowNano := uint64(time.Now().UnixNano())

	// check1: node cannot be severely behind (prevent genuine sync issues)
	timeDiff := nowNano - bestBlock.NanoTimestamp()
	if bestBlock.NanoTimestamp() > nowNano {
		timeDiff = bestBlock.NanoTimestamp() - nowNano
	}

	// if time difference is too large, do not allow proposals
	if timeDiff > ssm.maxTimeLag*3 {
		ssm.logger.Debug("unsafe to propose - severe time lag",
			"timeDiff", timeDiff,
			"maxAllowed", ssm.maxTimeLag*3)
		return false
	}

	// check2: ensure we have valid local state
	bestQC := ssm.chain.BestQC()
	if bestQC == nil || bestBlock == nil {
		ssm.logger.Debug("unsafe to propose - missing essential data")
		return false
	}

	// in other cases, allow proposals (including possible fork situations)
	// let the network consensus mechanism decide which is the correct chain
	return true
}
