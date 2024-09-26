// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package powpool

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"github.com/btcsuite/btcd/rpcclient"
	"github.com/ethereum/go-ethereum/event"
	"github.com/meterio/meter-pov/block"
	"github.com/meterio/meter-pov/builtin"
	"github.com/meterio/meter-pov/chain"
	"github.com/meterio/meter-pov/co"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/state"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	GlobPowPoolInst *PowPool

	powBlockRecvedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pow_block_recved",
		Help: "Accumulated counter for received pow blocks since last k-block",
	})
)

// Options options for tx pool.
type Options struct {
	Node            string
	Port            int
	User            string
	Pass            string
	Limit           int
	LimitPerAccount int
	MaxLifetime     time.Duration
}

type PowPoolStatus struct {
	Status       string
	KFrameHeight uint32
	LatestHeight uint32
	PoolSize     int
}

type PowReward struct {
	Rewarder meter.Address
	Value    big.Int
}

// pow decisions
type PowResult struct {
	Nonce         uint32
	Rewards       []PowReward
	Difficaulties *big.Int
	Raw           []block.PowRawBlock
}

// PowBlockEvent will be posted when pow is added or status changed.
type PowBlockEvent struct {
	BlockInfo *PowBlockInfo
}

// PowPool maintains unprocessed transactions.
type PowPool struct {
	chain        *chain.Chain
	stateCreator *state.Creator
	options      Options
	all          *powObjectMap
	replaying    bool

	done      chan struct{}
	powFeed   event.Feed
	scope     event.SubscriptionScope
	rpcClient *rpcclient.Client
	goes      co.Goes
}

func SetGlobPowPoolInst(pool *PowPool) bool {
	GlobPowPoolInst = pool
	return true
}

func GetGlobPowPoolInst() *PowPool {
	return GlobPowPoolInst
}

// New create a new PowPool instance.
// Shutdown is required to be called at end.
func New(options Options, chain *chain.Chain, stateCreator *state.Creator) *PowPool {
	pool := &PowPool{
		chain:        chain,
		stateCreator: stateCreator,
		replaying:    false,
		options:      options,
		all:          newPowObjectMap(),
		done:         make(chan struct{}),
	}
	pool.goes.Go(pool.housekeeping)
	SetGlobPowPoolInst(pool)
	prometheus.MustRegister(powBlockRecvedGauge)
	pool.initRpcClient()

	return pool
}

func (p *PowPool) housekeeping() {
}

// Close cleanup inner go routines.
func (p *PowPool) Close() {
	close(p.done)
	p.scope.Close()
	p.goes.Wait()
	slog.Debug("closed")
}

// SubscribePowBlockEvent receivers will receive a pow
func (p *PowPool) SubscribePowBlockEvent(ch chan *PowBlockEvent) event.Subscription {
	return p.scope.Track(p.powFeed.Subscribe(ch))
}

func (p *PowPool) InitialAddKframe(newPowBlockInfo *PowBlockInfo) error {
	err := p.Wash()
	if err != nil {
		return err
	}

	powObj := NewPowObject(newPowBlockInfo)
	// disable gossip
	//p.goes.Go(func() {
	//	p.powFeed.Send(&PowBlockEvent{BlockInfo: newPowBlockInfo})
	//})

	// send block to POW
	// raw := newPowBlockInfo.Raw
	// blks := bytes.Split(raw, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	// if len(blks) == 2 {
	powHex := hex.EncodeToString(newPowBlockInfo.PowRaw)
	posHex := hex.EncodeToString(newPowBlockInfo.PosRaw)
	go p.submitPosKblock(powHex, posHex)
	// } else {
	// fmt.Println("not enough items in raw block")
	// }

	return p.all.InitialAddKframe(powObj)
}

type RPCData struct {
	Jsonrpc string   `json:"jsonrpc"`
	Id      string   `json:"id"`
	Method  string   `json:"method"`
	Params  []string `json:"params"`
}

func (p *PowPool) submitPosKblock(powHex, posHex string) (string, string) {
	client := &http.Client{}

	data := &RPCData{
		Jsonrpc: "1.0",
		Id:      "test-id",
		Method:  "submitposkblock",
		Params:  []string{powHex, posHex},
	}
	b, err := json.Marshal(data)
	if err != nil {
		slog.Error("could not marshal json, error:", "err", err)
		return "", ""
	}

	url := fmt.Sprintf("http://%v:%v", p.options.Node, p.options.Port)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		slog.Error("could not create request", "err", err)
		return "", ""
	}

	auth := fmt.Sprintf("%v:%v", p.options.User, p.options.Pass)
	authToken := base64.StdEncoding.EncodeToString([]byte(auth))

	req.Header.Add("Authorization", "Basic "+authToken)
	req.Header.Set("Content-Type", "text/plain")

	res, err := client.Do(req)
	if err != nil {
		slog.Warn("Post kblock failed", "url=", url)
		return "", ""
	}

	tmp := make([]byte, 1)
	content := make([]byte, 0)
	i, err := res.Body.Read(tmp)
	for i > 0 && err == nil {
		i, err = res.Body.Read(tmp)
		content = append(content, tmp...)
	}
	return res.Status, string(content)
}

// Add add new pow block into pool.
// It's not assumed as an error if the pow to be added is already in the pool,
func (p *PowPool) Add(newPowBlockInfo *PowBlockInfo) error {
	if p.all.Contains(newPowBlockInfo.HeaderHash) {
		// pow already in the pool
		slog.Debug("PowPool Add, hash already in PowPool", "hash", newPowBlockInfo.HeaderHash)
		return nil
	}

	// disable powpool gossip
	//p.goes.Go(func() {
	//	p.powFeed.Send(&PowBlockEvent{BlockInfo: newPowBlockInfo})
	//})
	powObj := NewPowObject(newPowBlockInfo)
	err := p.all.Add(powObj)

	// if parent is not genesis and it's not contained in powpool
	// fetch the block immediately in a coroutine
	// Here err is set ONLY kframe is not added (not in committee).
	// a fat chance --- the powObj is already in chain, the parent block fetch is still sent.
	if err == nil && p.all.isKframeInitialAdded() && powObj.Height() > p.all.lastKframePowObj.Height() && !p.all.Contains(powObj.blockInfo.HashPrevBlock) {
		// go p.FetchPowBlock(powObj.Height() - uint32(1))
		slog.Info("Replay POW due to", "kframeAdded", p.all.isKframeInitialAdded(), "powObjHeight", powObj.Height(), "lastKframeHeight", p.all.lastKframePowObj.Height(), "containHashPrevBlock", p.all.Contains(powObj.blockInfo.HashPrevBlock))
		p.FetchBlock(powObj.Height() - 1)
	}

	return err
}

// Remove removes powObj from pool by its ID.
func (p *PowPool) Remove(powID meter.Bytes32) bool {
	if p.all.Remove(powID) {
		slog.Debug("pow header removed", "id", powID)
		return true
	}
	return false
}

func (p *PowPool) Wash() error {
	p.all.Flush()
	slog.Info("Powpool wash")
	return nil
}

// ==============APIs for consensus ===================
func NewPowResult(nonce uint32) *PowResult {
	return &PowResult{
		Nonce:         nonce,
		Difficaulties: big.NewInt(0),
	}
}

// consensus APIs
func (p *PowPool) GetPowDecision() (bool, *PowResult) {
	var mostDifficultResult *PowResult = nil

	// cases can not be decided
	if !p.all.isKframeInitialAdded() {
		slog.Debug("Not ready for KBlock: first kframe in epoch is missing")
		return false, nil
	}
	latestHeight := p.all.GetLatestHeight()
	lastKframeHeight := p.all.lastKframePowObj.Height()
	if (latestHeight < lastKframeHeight) ||
		((latestHeight - lastKframeHeight) < meter.NPowBlockPerEpoch) {
		slog.Debug("Not ready for KBlock (my POW height is too low or not enough powblocks in this epoch)", "latestHeight", latestHeight, "lastKframeHeight", lastKframeHeight)
		return false, nil
	}

	// Now have enough info to process
	for _, latestObj := range p.all.GetLatestObjects() {
		if latestObj == nil {
			continue
		}
		result, err := p.all.FillLatestObjChain(latestObj)
		if err != nil {
			fmt.Print(err)
			continue
		}

		if mostDifficultResult == nil {
			mostDifficultResult = result
		} else {
			if result.Difficaulties.Cmp(mostDifficultResult.Difficaulties) == 1 {
				mostDifficultResult = result
			}
		}
	}

	if mostDifficultResult == nil {
		slog.Debug("Not ready for KBlock : no result for most difficult chain")
		return false, nil
	} else {
		slog.Debug("Ready to propose KBlock", "latestHeight", latestHeight, "lastKframeHeight", lastKframeHeight)
		return true, mostDifficultResult
	}
}

func (p *PowPool) GetStatus() PowPoolStatus {
	s := PowPoolStatus{Status: "ok", LatestHeight: 0, KFrameHeight: 0, PoolSize: 0}
	// cases can not be decided
	if p.all != nil {
		s.LatestHeight = p.all.GetLatestHeight()
		s.PoolSize = p.all.Size()
	} else {
		s.Status = "object map is nil"
		return s
	}

	if !p.all.isKframeInitialAdded() {
		slog.Info("GetPowDecision false: kframe is not initially added")
		s.Status = "kframe is not initially added"
	} else {

		s.KFrameHeight = p.all.lastKframePowObj.Height()
	}
	return s
}

func (p *PowPool) VerifyNPowBlockPerEpoch() bool {
	// cases can not be decided
	if !p.all.isKframeInitialAdded() {
		slog.Info("GetPowDecision false: kframe is not initially added")
		return true
	}

	latestHeight := p.all.GetLatestHeight()
	lastKframeHeight := p.all.lastKframePowObj.Height()
	if latestHeight == 0 || lastKframeHeight == 0 {
		return true
	}

	if (latestHeight < lastKframeHeight) ||
		((latestHeight - lastKframeHeight) < meter.NPowBlockPerEpoch) {
		slog.Info("GetPowDecision false", "latestHeight", latestHeight, "lastKframeHeight", lastKframeHeight)
		return false
	}

	return true
}

// func (p *PowPool) FetchPowBlock(heights ...uint32) error {
// 	host := fmt.Sprintf("%v:%v", p.options.Node, p.options.Port)
// 	client, err := rpcclient.New(&rpcclient.ConnConfig{
// 		HTTPPostMode: true,
// 		DisableTLS:   true,
// 		Host:         host,
// 		User:         p.options.User,
// 		Pass:         p.options.Pass,
// 	}, nil)
// 	if err != nil {
// 		slog.Error("error creating new btc client", "err", err)
// 		return err
// 	}
// 	for _, height := range heights {
// 		hash, err := client.GetBlockHash(int64(height))
// 		if err != nil {
// 			slog.Error("error getting block hash", "err", err)
// 			continue
// 		}
// 		blk, err := client.GetBlock(hash)
// 		if err != nil {
// 			slog.Error("error getting block", "err", err)
// 			continue
// 		}
// 		info := NewPowBlockInfoFromPowBlock(blk)
// 		Err := p.Add(info)
// 		if Err != nil {
// 			slog.Error("add to pool failed", "err", Err)
// 			return Err
// 		}
// 	}
// 	return nil
// }

func (p *PowPool) initRpcClient() {
	host := fmt.Sprintf("%v:%v", p.options.Node, p.options.Port)
	slog.Info("init bitcoin rpc client", "host", host)
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		HTTPPostMode: true,
		DisableTLS:   true,
		Host:         host,
		User:         p.options.User,
		Pass:         p.options.Pass,
	}, nil)
	if err != nil {
		slog.Error("error creating new btc client", "err", err)
	}
	p.rpcClient = client
}

func (p *PowPool) WaitForSync() error {
	if p.rpcClient == nil {
		p.initRpcClient()
	}

	hash, err := p.rpcClient.GetBestBlockHash()
	if err != nil {
		slog.Error("error occured during getbestblockhash", "err", err)
		p.initRpcClient()
		return err
	}

	headerVerbose, err := p.rpcClient.GetBlockHeaderVerbose(hash)
	if err != nil {
		slog.Error("error occured during getblockheaderverbose", "err", err)
		p.initRpcClient()
		return err
	}
	pool := GetGlobPowPoolInst()

	for int32(pool.all.GetLatestHeight()) < headerVerbose.Height {
		time.Sleep(time.Second * 2)
		slog.Info("still waiting for sync", "to", headerVerbose.Height, "current", pool.all.GetLatestHeight())
	}
	slog.Info("Powpool is synced", "latest", headerVerbose.Height)
	return nil
}

func (p *PowPool) FetchBlock(height uint32) error {
	if p.rpcClient == nil {
		p.initRpcClient()
	}
	hash, err := p.rpcClient.GetBlockHash(int64(height))
	if err != nil {
		slog.Error("error getting block hash", "err", err)
		p.initRpcClient()
		return err
	}
	if p.all.Contains(meter.BytesToBytes32(hash.CloneBytes())) {
		slog.Info("skip hash", height, hex.EncodeToString(hash[:]))
		height++
		return nil
	}
	blk, err := p.rpcClient.GetBlock(hash)
	if err != nil {
		slog.Error("error getting block", "err", err)
		p.initRpcClient()
		return err
	}
	slog.Info("get pow block", "height", height)
	info := NewPowBlockInfoFromPowBlock(blk)
	err = p.Add(info)
	if err != nil {
		slog.Error("add to pool failed", "err", err)
		return err
	}
	return nil
}

func (p *PowPool) ReplayFrom(startHeight int32) error {
	if p.replaying {
		return nil
	}
	p.replaying = true
	defer func() {
		p.replaying = false
	}()

	if p.rpcClient == nil {
		p.initRpcClient()
	}

	hash, err := p.rpcClient.GetBestBlockHash()
	if err != nil {
		slog.Error("error occured during getbestblockhash", "err", err)
		p.initRpcClient()
		return err
	}

	headerVerbose, err := p.rpcClient.GetBlockHeaderVerbose(hash)
	if err != nil {
		slog.Error("error occured during getblockheaderverbose", "err", err)
		p.initRpcClient()
		return err
	}
	pool := GetGlobPowPoolInst()
	height := startHeight

	if startHeight <= headerVerbose.Height {
		slog.Info("Pow replay started", "start", startHeight, "end", headerVerbose.Height)
	}
	for height <= headerVerbose.Height {
		if height < int32(p.all.GetLatestHeight()) {
			slog.Info("skip height", height)
			height++
			continue
		}
		hash, err := p.rpcClient.GetBlockHash(int64(height))
		if err != nil {
			slog.Error("error getting block hash", "err", err)
			p.initRpcClient()
			return err
		}
		if p.all.Contains(meter.BytesToBytes32(hash.CloneBytes())) {
			slog.Info("skip hash", height, hex.EncodeToString(hash[:]))
			height++
			continue
		}
		blk, err := p.rpcClient.GetBlock(hash)
		if err != nil {
			slog.Error("error getting block", "err", err)
			p.initRpcClient()
			return err
		}
		slog.Info("get pow block", "height", height)
		info := NewPowBlockInfoFromPowBlock(blk)
		Err := pool.Add(info)
		if Err != nil {
			slog.Error("add to pool failed", "err", Err)
			return Err
		}
		height++
	}
	slog.Info("Pow replay done", "start", startHeight, "end", headerVerbose.Height)
	p.replaying = false
	return nil
}

func (pool *PowPool) GetCurCoef() (curCoef int64) {
	if pool == nil {
		panic("get globalPowPool failed")
	}
	bestBlock := pool.chain.BestBlock()
	epoch := uint64(bestBlock.GetBlockEpoch())

	state, err := pool.stateCreator.NewState(bestBlock.Header().StateRoot())
	if err != nil {
		panic("get state failed")
	}
	bigCoef := builtin.Params.Native(state).Get(meter.KeyPowPoolCoef)
	coef := bigCoef.Int64()

	// builtin parameter has uint of wei, aks, 1e18, so divide by 1e9 twice
	d := builtin.Params.Native(state).Get(meter.KeyPowPoolCoefFadeDays)
	d = d.Div(d, big.NewInt(1e09))
	fd := new(big.Float).SetInt(d)
	fadeDays, _ := fd.Float64()
	fadeDays = fadeDays / (1e09)

	// builtin fade rate
	r := builtin.Params.Native(state).Get(meter.KeyPowPoolCoefFadeRate)
	r = r.Div(r, big.NewInt(1e09))
	fr := new(big.Float).SetInt(r)
	fadeRate, _ := fr.Float64()
	fadeRate = fadeRate / (1e09)

	slog.Debug("GetCurCoef", "coef", coef, "epoch", epoch, "fadeDays", fadeDays, "fadeRate", fadeRate)
	curCoef = calcPowCoef(0, epoch, coef, fadeDays, fadeRate)
	slog.Debug("Current Coef:", "curCoef", curCoef)
	return curCoef
}

func (p *PowPool) Len() int {
	return p.all.Len()
}
