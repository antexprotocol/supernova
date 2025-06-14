package consensus

import (
	"context"
	"log/slog"
	"time"

	"github.com/antexprotocol/supernova/chain"
	"github.com/antexprotocol/supernova/libs/p2p"
	"github.com/antexprotocol/supernova/libs/rpc"
	"github.com/antexprotocol/supernova/types"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	SyncCheckInterval = 30 * time.Second // sync check interval
	MaxBlockLag       = 5                // max allowed block lag
	SyncTimeout       = 10 * time.Minute // sync timeout
)

// SyncChecker responsible for continuously checking and repairing node sync status
type SyncChecker struct {
	ctx    context.Context
	chain  *chain.Chain
	p2pSrv p2p.P2P
	rpc    *rpc.RPCServer
	logger *slog.Logger

	lastSyncCheck time.Time
	isSyncing     bool
}

// NewSyncChecker create a new sync checker
func NewSyncChecker(ctx context.Context, chain *chain.Chain, p2pSrv p2p.P2P, rpc *rpc.RPCServer) *SyncChecker {
	return &SyncChecker{
		ctx:           ctx,
		chain:         chain,
		p2pSrv:        p2pSrv,
		rpc:           rpc,
		logger:        slog.With("pkg", "sync_checker"),
		lastSyncCheck: time.Now(),
	}
}

// Start start sync checker
func (sc *SyncChecker) Start() {
	// Comment out for now to avoid sync check
	// go sc.run()
}

// run main loop
func (sc *SyncChecker) run() {
	ticker := time.NewTicker(SyncCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sc.ctx.Done():
			return
		case <-ticker.C:
			sc.checkAndSync()
		}
	}
}

// checkAndSync check sync status and start sync if needed
func (sc *SyncChecker) checkAndSync() {
	if sc.isSyncing {
		sc.logger.Debug("sync already in progress, skipping check")
		return
	}

	peers := sc.p2pSrv.Peers().All()
	if len(peers) == 0 {
		sc.logger.Debug("no peers available for sync check")
		return
	}

	bestBlock := sc.chain.BestBlock()
	bestQC := sc.chain.BestQC()
	nowNano := uint64(time.Now().UnixNano())

	// check time sync
	timeDiff := nowNano - bestBlock.NanoTimestamp()
	if bestBlock.NanoTimestamp() > nowNano {
		timeDiff = bestBlock.NanoTimestamp() - nowNano
	}

	// check QC consistency
	qcMatches := bestQC.BlockID == bestBlock.ID()

	// check if sync is needed
	needsSync := false

	if timeDiff > types.BlockInterval*MaxBlockLag {
		sc.logger.Warn("time lag detected", "timeDiff", timeDiff, "maxAllowed", types.BlockInterval*MaxBlockLag)
		needsSync = true
	}

	if !qcMatches {
		sc.logger.Warn("QC mismatch detected", "bestBlockID", bestBlock.ID(), "bestQCBlockID", bestQC.BlockID)
		needsSync = true
	}

	// check peer status to determine if we are behind
	if !needsSync {
		needsSync = sc.checkPeerStatus(peers, bestBlock.Number())
	}

	if needsSync {
		sc.logger.Info("sync needed, starting sync process")
		go sc.performSync(peers)
	} else {
		sc.logger.Debug("sync check passed")
	}

	sc.lastSyncCheck = time.Now()
}

// checkPeerStatus check peer status to determine if we are behind
func (sc *SyncChecker) checkPeerStatus(peers []peer.ID, currentHeight uint32) bool {
	higherPeers := 0

	for _, peerID := range peers {
		sc.logger.Debug("checking peer status", "peer", peerID)

		// try to get higher height blocks to determine if we are behind
		blocks, err := sc.rpc.GetBlocksFromNumber(peerID, currentHeight+1)
		if err == nil && len(blocks) > 0 {
			higherPeers++
			sc.logger.Debug("peer has higher blocks", "peer", peerID, "nextBlock", currentHeight+1)
		}
	}

	// if more than half of the peers have higher blocks, consider sync needed
	return higherPeers > len(peers)/2
}

// performSync perform sync
func (sc *SyncChecker) performSync(peers []peer.ID) {
	sc.isSyncing = true
	defer func() {
		sc.isSyncing = false
	}()

	ctx, cancel := context.WithTimeout(sc.ctx, SyncTimeout)
	defer cancel()

	startHeight := sc.chain.BestBlock().Number() + 1
	sc.logger.Info("starting sync", "fromHeight", startHeight)

	// try to sync from each peer
	for _, peerID := range peers {
		select {
		case <-ctx.Done():
			sc.logger.Warn("sync timeout or cancelled")
			return
		default:
		}

		if sc.syncFromPeer(ctx, peerID, startHeight) {
			sc.logger.Info("sync completed successfully", "peer", peerID)
			return
		}
	}

	sc.logger.Warn("failed to sync from any peer")
}

// syncFromPeer sync from specified peer
func (sc *SyncChecker) syncFromPeer(ctx context.Context, peerID peer.ID, fromHeight uint32) bool {
	sc.logger.Info("syncing from peer", "peer", peerID, "fromHeight", fromHeight)

	// batch get blocks
	for height := fromHeight; ; height += 10 {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		blocks, err := sc.rpc.GetBlocksFromNumber(peerID, height)
		if err != nil {
			sc.logger.Debug("failed to get blocks from peer", "peer", peerID, "height", height, "err", err)
			return false
		}

		if len(blocks) == 0 {
			sc.logger.Debug("no more blocks to sync", "lastHeight", height)
			return true
		}

		// process the retrieved blocks
		for _, block := range blocks {
			sc.logger.Debug("synced block", "height", block.Block.Number(), "id", block.Block.ID())
		}

		// if the number of retrieved blocks is less than expected, it means sync is complete
		if len(blocks) < 10 {
			return true
		}
	}
}

// GetSyncStatus get sync status
func (sc *SyncChecker) GetSyncStatus() (bool, time.Time) {
	return sc.isSyncing, sc.lastSyncCheck
}
