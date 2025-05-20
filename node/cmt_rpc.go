package node

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/antexprotocol/supernova/types"
	abci "github.com/cometbft/cometbft/abci/types"
	v1 "github.com/cometbft/cometbft/api/cometbft/abci/v1"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	"github.com/cometbft/cometbft/proxy"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
)

const (
	defaultPerPage = 30
	maxPerPage     = 100
)

func (*Node) isChainSynced(nowTimestamp, blockTimestamp uint64) bool {
	timeDiff := nowTimestamp - blockTimestamp
	if blockTimestamp > nowTimestamp {
		timeDiff = blockTimestamp - nowTimestamp
	}
	return timeDiff < types.BlockInterval*6
}

func (*Node) validatePerPage(perPagePtr *int) int {
	if perPagePtr == nil { // no per_page parameter
		return defaultPerPage
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage
	} else if perPage > maxPerPage {
		return maxPerPage
	}
	return perPage
}

func (*Node) validatePage(pagePtr *int, perPage, totalCount int) (int, error) {
	if perPage < 1 {
		panic(fmt.Sprintf("zero or negative perPage: %d", perPage))
	}

	if pagePtr == nil { // no page parameter
		return 1, nil
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page (even if it's empty)
	}
	page := *pagePtr
	if page <= 0 || page > pages {
		return 1, fmt.Errorf("page should be within [1, %d] range, given %d", pages, page)
	}

	return page, nil
}

func (*Node) validateSkipCount(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}

	return skipCount
}

func (n *Node) BroadcastTxSync(ctx *rpctypes.Context, tx cmttypes.Tx) (*ctypes.ResultBroadcastTx, error) {
	res, err := n.proxyApp.Mempool().CheckTx(ctx.Context(), &abci.CheckTxRequest{
		Tx:   tx,
		Type: v1.CHECK_TX_TYPE_CHECK,
	})
	if err != nil {
		n.logger.Error("BroadcastTxSync checkTx", "error", err)
		return nil, err
	}
	n.logger.Info("BroadcastTxSync checkTx", "hash", tx.Hash(), "result", res.String())
	// fmt.Println("BroadcastTxSync", "hash", tx.Hash(), "result", res.String())

	// pid, err := peer.IDFromBytes(n.nodeKey.PrivKey.Bytes())
	// if err != nil {
	// 	n.logger.Error("BroadcastTxSync IDFromBytes", "error", err)
	// 	return nil, err
	// }

	// err = n.rpc.NotifyNewTx(pid, tx)
	err = n.txPool.StrictlyAdd(tx)
	if err != nil {
		n.logger.Error("BroadcastTxSync NotifyNewTx", "error", err)
		return nil, err
	}
	n.logger.Info("BroadcastTxSync", "hash", hex.EncodeToString(tx.Hash()))

	return &ctypes.ResultBroadcastTx{
		Code:      0,
		Data:      res.Data,
		Log:       res.Log,
		Codespace: res.Codespace,
		Hash:      tx.Hash(),
	}, nil
}

func (n *Node) BroadcastTxAsync(ctx *rpctypes.Context, tx cmttypes.Tx) (*ctypes.ResultBroadcastTx, error) {
	return n.BroadcastTxSync(ctx, tx)
}

func (n *Node) Tx(ctx *rpctypes.Context, hash []byte, prove bool) (*ctypes.ResultTx, error) {
	// if _, ok := env.TxIndexer.(*null.TxIndex); ok {
	// 	return nil, errors.New("transaction indexing is disabled")
	// }

	// r, err := n.rpc.Get(hash)
	// if err != nil {
	// 	return nil, err
	// }

	// if r == nil {
	// 	return nil, fmt.Errorf("tx (%X) not found", hash)
	// }

	// var proof types.TxProof
	// if prove {
	// 	block, _ := env.BlockStore.LoadBlock(r.Height)
	// 	if block != nil {
	// 		proof = block.Data.Txs.Proof(int(r.Index))
	// 	}
	// }
	return nil, nil
}

func (n *Node) RpcHealth(*rpctypes.Context) (*ctypes.ResultHealth, error) {
	return &ctypes.ResultHealth{}, nil
}

func (n *Node) RpcStatus(*rpctypes.Context) (*ctypes.ResultStatus, error) {
	var (
		earliestBlockHeight   int64
		earliestBlockHash     cmtbytes.HexBytes
		earliestAppHash       cmtbytes.HexBytes
		earliestBlockTimeNano int64
	)

	genesisBlock := n.chain.GenesisBlock()
	bestBlock := n.chain.BestBlock()
	genesisBlockId := genesisBlock.Header().ID()
	bestBlockId := bestBlock.Header().ID()

	if genesisBlock != nil {
		earliestBlockHeight = int64(genesisBlock.Number())
		earliestAppHash = genesisBlock.AppHash()
		earliestBlockHash = genesisBlockId[:]
		earliestBlockTimeNano = int64(genesisBlock.Timestamp())
	}

	var (
		latestBlockHash     cmtbytes.HexBytes
		latestAppHash       cmtbytes.HexBytes
		latestBlockTimeNano int64

		latestHeight = int64(bestBlock.Number())
	)

	if latestHeight != 0 {
		latestBlockHash = bestBlockId[:]
		latestAppHash = bestBlock.Header().AppHash
		latestBlockTimeNano = int64(bestBlock.Timestamp())
	}

	// Return the very last voting power, not the voting power of this validator
	// during the last block.
	var votingPower int64
	vset := n.chain.GetBestValidatorSet()
	for i, val := range vset.Validators {
		if i == int(bestBlock.ProposerIndex()) {
			votingPower = val.VotingPower
			break
		}
	}

	result := &ctypes.ResultStatus{
		// NodeInfo: env.P2PTransport.NodeInfo().(p2p.DefaultNodeInfo),
		SyncInfo: ctypes.SyncInfo{
			LatestBlockHash:     latestBlockHash,
			LatestAppHash:       latestAppHash,
			LatestBlockHeight:   latestHeight,
			LatestBlockTime:     time.Unix(0, latestBlockTimeNano),
			EarliestBlockHash:   earliestBlockHash,
			EarliestAppHash:     earliestAppHash,
			EarliestBlockHeight: earliestBlockHeight,
			EarliestBlockTime:   time.Unix(0, earliestBlockTimeNano),
			CatchingUp:          !n.isChainSynced(uint64(time.Now().Unix()), uint64(bestBlock.Timestamp())),
		},
		ValidatorInfo: ctypes.ValidatorInfo{
			Address:     n.nodeKey.PubKey().Address(),
			PubKey:      n.nodeKey.PubKey(),
			VotingPower: votingPower,
		},
	}

	return result, nil
}

func (n *Node) RpcValidators(
	_ *rpctypes.Context,
	heightPtr *int64,
	pagePtr, perPagePtr *int,
) (*ctypes.ResultValidators, error) {
	// The latest validator that we know is the NextValidator of the last block.
	latestHeight := int64(n.chain.BestBlock().Number())
	queryHeight := latestHeight
	if heightPtr != nil {
		height := *heightPtr
		if height <= 0 {
			return nil, fmt.Errorf("height must be greater than 0, but got %d", height)
		}
		if height > latestHeight {
			return nil, fmt.Errorf("height %d must be less than or equal to the current blockchain height %d",
				height, latestHeight)
		}
		queryHeight = height
	}

	validators := n.chain.GetValidatorSet(uint32(queryHeight))

	totalCount := len(validators.Validators)
	perPage := n.validatePerPage(perPagePtr)
	page, err := n.validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := n.validateSkipCount(page, perPage)

	v := validators.Validators[skipCount : skipCount+cmtmath.MinInt(perPage, totalCount-skipCount)]

	return &ctypes.ResultValidators{
		BlockHeight: queryHeight,
		Validators:  v,
		Count:       len(v),
		Total:       totalCount,
	}, nil
}

func (n *Node) RpcBlock(_ *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlock, error) {
	latestHeight := int64(n.chain.BestBlock().Number())
	queryHeight := latestHeight
	if heightPtr != nil {
		height := *heightPtr
		if height <= 0 {
			return nil, fmt.Errorf("height must be greater than 0, but got %d", height)
		}
		if height > latestHeight {
			return nil, fmt.Errorf("height %d must be less than or equal to the current blockchain height %d",
				height, latestHeight)
		}
		queryHeight = height
	}

	block, err := n.chain.GetTrunkBlock(uint32(queryHeight))
	if err != nil {
		return nil, err
	}
	if block == nil {
		return &ctypes.ResultBlock{BlockID: cmttypes.BlockID{}, Block: nil}, nil
	}
	blockID := block.ID()
	return &ctypes.ResultBlock{BlockID: cmttypes.BlockID{
		Hash: blockID[:],
		PartSetHeader: cmttypes.PartSetHeader{
			Total: 1,
			Hash:  blockID[:],
		},
	}, Block: &cmttypes.Block{
		Header: cmttypes.Header{
			Height:  int64(block.Number()),
			AppHash: block.AppHash(),
			LastBlockID: cmttypes.BlockID{
				Hash: blockID[:],
				PartSetHeader: cmttypes.PartSetHeader{
					Total: 1,
					Hash:  blockID[:],
				},
			},
			ValidatorsHash: block.ValidatorsHash(),
		},
		Data: cmttypes.Data{
			Txs: cmttypes.Txs(block.Txs),
		},
		LastCommit: &cmttypes.Commit{
			Height: int64(block.Number()),
			BlockID: cmttypes.BlockID{
				Hash: blockID[:],
				PartSetHeader: cmttypes.PartSetHeader{
					Total: 1,
					Hash:  blockID[:],
				},
			},
			Round: int32(block.QC.Round),
		},
	}}, nil
}

func (n *Node) RpcBlockByHash(_ *rpctypes.Context, hash []byte) (*ctypes.ResultBlock, error) {
	var hash32 [32]byte
	copy(hash32[:], hash)
	block, err := n.chain.GetBlock(hash32)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return &ctypes.ResultBlock{BlockID: cmttypes.BlockID{}, Block: nil}, nil
	}
	blockID := block.ID()
	return &ctypes.ResultBlock{BlockID: cmttypes.BlockID{
		Hash: blockID[:],
		PartSetHeader: cmttypes.PartSetHeader{
			Total: 1,
			Hash:  blockID[:],
		},
	}, Block: &cmttypes.Block{
		Header: cmttypes.Header{
			Height:  int64(block.Number()),
			AppHash: block.AppHash(),
			LastBlockID: cmttypes.BlockID{
				Hash: blockID[:],
				PartSetHeader: cmttypes.PartSetHeader{
					Total: 1,
					Hash:  blockID[:],
				},
			},
			ValidatorsHash: block.ValidatorsHash(),
		},
		Data: cmttypes.Data{
			Txs: cmttypes.Txs(block.Txs),
		},
		LastCommit: &cmttypes.Commit{
			Height: int64(block.Number()),
			BlockID: cmttypes.BlockID{
				Hash: blockID[:],
				PartSetHeader: cmttypes.PartSetHeader{
					Total: 1,
					Hash:  blockID[:],
				},
			},
			Round: int32(block.QC.Round),
		},
	}}, nil
}

func (n *Node) RpcUnconfirmedTxs(_ *rpctypes.Context, limitPtr *int) (*ctypes.ResultUnconfirmedTxs, error) {
	// reuse per_page validator
	limit := n.validatePerPage(limitPtr)

	txs := n.txPool.ReapMaxTxs(limit)
	return &ctypes.ResultUnconfirmedTxs{
		Count:      len(txs),
		Total:      n.txPool.Len(),
		TotalBytes: n.txPool.LenBytes(),
		Txs:        txs,
	}, nil
}

func (n *Node) RpcNumUnconfirmedTxs(*rpctypes.Context) (*ctypes.ResultUnconfirmedTxs, error) {
	return &ctypes.ResultUnconfirmedTxs{
		Count:      n.txPool.Len(),
		Total:      n.txPool.Len(),
		TotalBytes: n.txPool.LenBytes(),
	}, nil
}

func (n *Node) RpcABCIQuery(
	_ *rpctypes.Context,
	path string,
	data cmtbytes.HexBytes,
	height int64,
	prove bool,
) (*ctypes.ResultABCIQuery, error) {
	resQuery, err := n.proxyApp.Query().Query(context.TODO(), &abci.QueryRequest{
		Path:   path,
		Data:   data,
		Height: height,
		Prove:  prove,
	})
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultABCIQuery{Response: *resQuery}, nil
}

func (n *Node) RpcABCIInfo(ctx *rpctypes.Context) (*ctypes.ResultABCIInfo, error) {
	resInfo, err := n.proxyApp.Query().Info(context.TODO(), proxy.InfoRequest)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultABCIInfo{Response: *resInfo}, nil
}
