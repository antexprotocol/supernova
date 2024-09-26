// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package blocks

import (
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/ethereum/go-ethereum/common/math"
	"github.com/gorilla/mux"
	"github.com/meterio/meter-pov/api/utils"
	"github.com/meterio/meter-pov/block"
	"github.com/meterio/meter-pov/builtin"
	"github.com/meterio/meter-pov/chain"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/state"
	"github.com/meterio/meter-pov/tx"
	"github.com/pkg/errors"
)

type Blocks struct {
	chain  *chain.Chain
	stateC *state.Creator
	logger *slog.Logger
}

func New(chain *chain.Chain, stateC *state.Creator) *Blocks {
	return &Blocks{
		chain,
		stateC,
		slog.With("api", "blk"),
	}
}

func (b *Blocks) handleGetBlock(w http.ResponseWriter, req *http.Request) error {
	revision, err := b.parseRevision(mux.Vars(req)["revision"])
	if err != nil {
		return utils.BadRequest(errors.WithMessage(err, "revision"))
	}
	expanded := req.URL.Query().Get("expanded")
	if expanded != "" && expanded != "false" && expanded != "true" {
		return utils.BadRequest(errors.WithMessage(errors.New("should be boolean"), "expanded"))
	}

	block, err := b.getBlock(revision)
	if err != nil {
		if b.chain.IsNotFound(err) {
			return utils.WriteJSON(w, nil)
		}
		return err
	}
	isTrunk, err := b.isTrunk(block.ID(), block.Number())
	if err != nil {
		return err
	}

	var receipts tx.Receipts
	if block.ID().String() == b.chain.GenesisBlock().ID().String() {
		// if is genesis

	} else {
		receipts, err = b.chain.GetBlockReceipts(block.ID())
		if err != nil {
			// ignore errors
			receipts = make([]*tx.Receipt, 0)
		}
	}
	bloom := tx.CreateEthBloom(receipts)
	logsBloom := "0x" + hex.EncodeToString(bloom.Bytes())
	s, e := b.stateC.NewState(block.StateRoot())
	if e != nil {
		return e
	}
	baseGasFee := builtin.Params.Native(s).Get(meter.KeyBaseGasPrice)
	jSummary := buildJSONBlockSummary(block, isTrunk, logsBloom, baseGasFee)
	if expanded == "true" {
		var receipts tx.Receipts
		var err error
		var txs tx.Transactions
		if block.ID().String() == b.chain.GenesisBlock().ID().String() {
			// if is genesis

		} else {
			txs = block.Txs
			receipts, err = b.chain.GetBlockReceipts(block.ID())
			if err != nil {
				return err
			}
		}

		return utils.WriteJSON(w, &JSONExpandedBlock{
			jSummary,
			buildJSONEmbeddedTxs(txs, receipts, baseGasFee),
		})
	}
	txIds := make([]meter.Bytes32, 0)
	for _, tx := range block.Txs {
		txIds = append(txIds, tx.ID())
	}
	return utils.WriteJSON(w, &JSONCollapsedBlock{jSummary, txIds})
}

func (b *Blocks) parseRevision(revision string) (interface{}, error) {
	if revision == "" || revision == "best" {
		return nil, nil
	}
	if len(revision) == 66 || len(revision) == 64 {
		blockID, err := meter.ParseBytes32(revision)
		if err != nil {
			return nil, err
		}
		return blockID, nil
	}
	n, err := strconv.ParseUint(revision, 0, 0)
	if err != nil {
		return nil, err
	}
	if n > math.MaxUint32 {
		return nil, errors.New("block number out of max uint32")
	}
	return uint32(n), err
}

func (b *Blocks) parseEpoch(epoch string) (uint32, error) {
	n, err := strconv.ParseUint(epoch, 0, 0)
	if err != nil {
		return 0, err
	}
	if n > math.MaxUint32 {
		return 0, errors.New("block number out of max uint32")
	}
	return uint32(n), err
}

func (b *Blocks) getBlock(revision interface{}) (*block.Block, error) {
	switch revision.(type) {
	case meter.Bytes32:
		blk, err := b.chain.GetBlock(revision.(meter.Bytes32))
		if err != nil {
			return blk, err
		}
		best := b.chain.BestBlock()
		if blk.Number() > best.Number() {
			return nil, chain.ErrNotFound
		}
		return blk, err
	case uint32:
		best := b.chain.BestBlock()
		if revision.(uint32) > best.Number() {
			return nil, chain.ErrNotFound
		}
		return b.chain.GetTrunkBlock(revision.(uint32))
	default:
		return b.chain.BestBlock(), nil
	}
}

func (b *Blocks) getKBlockByEpoch(epoch uint64) (*block.Block, error) {
	best := b.chain.BestBlock()
	curEpoch := best.GetBlockEpoch()
	if epoch > curEpoch {
		b.logger.Warn("requested epoch is too new", "epoch", epoch)
		return nil, errors.New("requested epoch is too new")
	}

	//b.logger.Info("getKBlockByEpoch", "epoch", epoch, "curEpoch", curEpoch)
	delta := uint64(best.Number()) / curEpoch

	var blk *block.Block
	var ht, ep uint64
	var err error
	if curEpoch-epoch <= 5 {
		blk = best
		ht = uint64(best.Number())
		ep = curEpoch
	} else {
		ht := delta * (epoch + 4)
		blk, err = b.chain.GetTrunkBlock(uint32(ht))
		if err != nil {
			b.logger.Error("get kblock failed", "epoch", epoch, "err", err)
			return nil, err
		}

		ep = blk.GetBlockEpoch()
		for ep < epoch+1 {
			ht = ht + (4 * delta)
			if ht >= uint64(best.Number()) {
				ht = uint64(best.Number())
				blk = best
				ep = curEpoch
				break
			}

			blk, err = b.chain.GetTrunkBlock(uint32(ht) + uint32(5*delta))
			if err != nil {
				b.logger.Error("get kblock failed", "epoch", epoch, "err", err)
				return nil, err
			}
			ep = blk.GetBlockEpoch()
			//b.logger.Info("... height:", blk.Number(), "epoch", ep)
		}
	}

	// find out the close enough search point
	// b.logger.Info("start to search kblock", "height:", blk.Number(), "epoch", ep, "target epoch", epoch)
	for ep > epoch {
		blk, err = b.chain.GetTrunkBlock(blk.LastKBlockHeight())
		if err != nil {
			b.logger.Error("getTrunkBlock failed", "epoch", epoch, "err", err)
			return nil, err
		}
		ep = blk.GetBlockEpoch()
		//b.logger.Info("...searching height:", blk.Number(), "epoch", ep)
	}

	//b.logger.Info("get the kblock", "height:", blk.Number(), "epoch", ep)
	if ep == epoch {
		return blk, nil
	}

	// now ep < epoch for some reason.
	ht = uint64(blk.Number() + 1)
	count := 0
	for {
		blk, err = b.chain.GetTrunkBlock(uint32(ht))
		if err != nil {
			b.logger.Error("getTrunkBlock failed", "epoch", epoch, "err", err)
			return nil, err
		}
		ep = blk.GetBlockEpoch()

		if ep == epoch && blk.IsKBlock() {
			return blk, nil // find it !!!
		}
		if ep > epoch {
			return nil, errors.New("can not find the kblock")
		}

		count++
		if count >= 2000 {
			return nil, errors.New("can not find the kblock")
		}
		//b.logger.Info("...final search. height:", ht, "epoch", ep)
	}
}

func (b *Blocks) isTrunk(blkID meter.Bytes32, blkNum uint32) (bool, error) {
	best := b.chain.BestBlock()
	ancestorID, err := b.chain.GetAncestorBlockID(best.ID(), blkNum)
	if err != nil {
		return false, err
	}
	return ancestorID == blkID, nil
}

func (b *Blocks) handleGetQC(w http.ResponseWriter, req *http.Request) error {
	revision, err := b.parseRevision(mux.Vars(req)["revision"])
	if err != nil {
		return utils.BadRequest(errors.WithMessage(err, "revision"))
	}
	block, err := b.getBlock(revision)
	if err != nil {
		if b.chain.IsNotFound(err) {
			return utils.WriteJSON(w, nil)
		}
		return err
	}
	_, err = b.isTrunk(block.ID(), block.Number())
	if err != nil {
		return err
	}
	qc, err := convertQCWithRaw(block.QC)
	if err != nil {
		return err
	}
	return utils.WriteJSON(w, qc)
}

func (b *Blocks) handleGetEpochPowInfo(w http.ResponseWriter, req *http.Request) error {
	epoch, err := b.parseEpoch(mux.Vars(req)["epoch"])
	if err != nil {
		return utils.BadRequest(errors.WithMessage(err, "epoch"))
	}

	block, err := b.getKBlockByEpoch(uint64(epoch))
	if err != nil {
		if b.chain.IsNotFound(err) {
			return utils.WriteJSON(w, nil)
		}
		return utils.BadRequest(errors.WithMessage(err, "can't locate kblock within epoch"))
	}

	jEpoch := buildJSONEpoch(block)
	if jEpoch == nil {
		return utils.BadRequest(errors.WithMessage(errors.New("json marshal"), "can't marshal json object for epoch"))
	}

	return utils.WriteJSON(w, jEpoch)
}

func (b *Blocks) handleGetBaseFee(w http.ResponseWriter, req *http.Request) error {
	headBlock := b.chain.BestBlock()
	if headBlock == nil {
		return errors.New("could not load latest block")
	}
	s, err := b.stateC.NewState(headBlock.StateRoot())
	if err != nil {
		return err
	}
	baseGasPrice := builtin.Params.Native(s).Get(meter.KeyBaseGasPrice)
	return utils.WriteJSON(w, baseGasPrice)
}

func (b *Blocks) Mount(root *mux.Router, pathPrefix string) {
	sub := root.PathPrefix(pathPrefix).Subrouter()
	sub.Path("/baseFee").Methods("GET").HandlerFunc(utils.WrapHandlerFunc(b.handleGetBaseFee))
	sub.Path("/qc/{revision}").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(b.handleGetQC))
	sub.Path("/{revision}").Methods("GET").HandlerFunc(utils.WrapHandlerFunc(b.handleGetBlock))
	sub.Path("/epoch/{epoch}").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(b.handleGetEpochPowInfo))
}
