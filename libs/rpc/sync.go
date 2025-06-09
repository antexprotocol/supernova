package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/antexprotocol/supernova/block"
	"github.com/antexprotocol/supernova/libs/co"
	"github.com/antexprotocol/supernova/types"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
)

const initSyncInterval = 500 * time.Millisecond
const syncInterval = 6 * time.Second
const activeSyncInterval = 30 * time.Second

// Sync start synchronization process.
func (s *RPCServer) Sync(handler HandleBlockStream) {
	s.goes.Go(func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		delay := initSyncInterval
		syncCount := 0
		lastSyncTime := time.Now()

		shouldSynced := func() bool {
			bestBlockTime := s.chain.BestBlock().Timestamp()
			now := uint64(time.Now().Unix())

			timeDiff := now - bestBlockTime
			if bestBlockTime > now {
				timeDiff = bestBlockTime - now
			}

			isSynced := timeDiff < types.BlockInterval*3 && s.chain.BestQC().BlockID == s.chain.BestBlock().ID()

			peers := s.p2pSrv.Peers().All()
			if len(peers) == 0 {
				s.logger.Debug("no peers available, assuming synced")
				return true
			}

			if isSynced && time.Since(lastSyncTime) > activeSyncInterval {
				s.logger.Debug("periodic sync check needed")
				return false
			}

			return isSynced
		}

		for {
			timer.Stop()
			timer = time.NewTimer(delay)
			select {
			case <-s.ctx.Done():
				s.logger.Warn("stop communicator due to context end")
				return
			case <-timer.C:
				s.logger.Debug("synchronization start")

				// best := s.chain.BestBlock().Header()
				// choose peer which has the head block with higher total score
				// FIXME: filter peers with best known number
				peers := s.p2pSrv.Peers().All()

				if len(peers) < 1 {
					s.logger.Debug("no suitable peer to sync")
					break
				} else {
					synced := false
					for _, peerID := range peers {
						if err := s.download(peerID, handler); err != nil {
							s.logger.Debug("synchronization failed", "peer", peerID, "err", err)
							continue
						}
						s.logger.Debug("synchronization done", "peer", peerID)
						synced = true
						lastSyncTime = time.Now()
						break
					}

					if !synced {
						s.logger.Warn("failed to sync from any peer")
					}
				}
				syncCount++

				if shouldSynced() {
					delay = syncInterval
					s.onceSynced.Do(func() {
						// s.Synced = true
						close(s.syncedCh)
					})
				} else {
					if delay > initSyncInterval*4 {
						delay = initSyncInterval * 2
					}
				}
			}
		}
	})
}

func (s *RPCServer) download(peerID peer.ID, handler HandleBlockStream) error {
	fromNum := s.chain.BestBlock().Number() + 1

	// it's important to set cap to 2
	errCh := make(chan error, 2)

	ctx, cancel := context.WithCancel(s.ctx)
	blockCh := make(chan *block.EscortedBlock, 4096)

	var goes co.Goes
	// block consumer
	goes.Go(func() {
		defer cancel()
		if err := handler(ctx, blockCh); err != nil {
			errCh <- err
		}
	})

	// block downloader
	goes.Go(func() {
		defer close(blockCh)
		var blocks []*block.EscortedBlock
		for {
			start := time.Now()
			result, err := s.GetBlocksFromNumber(peerID, fromNum)
			if err != nil {
				errCh <- err
				return
			}
			if len(result) > 0 {
				s.logger.Info(fmt.Sprintf("downloaded blocks(%d) from %d", len(result), fromNum), "peer", peerID, "elapsed", types.PrettyDuration(time.Since(start)))
			}
			if len(result) == 0 {
				return
			}

			blocks = blocks[:0]
			for _, blk := range result {
				if blk.Block.Number() != fromNum {
					errCh <- errors.New("broken sequence")
					return
				}
				fromNum++
				blocks = append(blocks, blk)
			}

			<-co.Parallel(func(queue chan<- func()) {
				for _, blk := range blocks {
					h := blk.Block.Header()
					queue <- func() { h.ID() }
					for _, tx := range blk.Block.Transactions() {
						tx := tx
						queue <- func() {
							tx.Hash()
						}
					}
				}
			})

			for _, blk := range blocks {
				select {
				case <-ctx.Done():
					return
				case blockCh <- blk:
					// log.Info("Put in block chan", "blk", blk.Block.Number(), "len", len(blockCh), "cap", cap(blockCh))
				}
			}
		}
	})
	goes.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}
