// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package node

import (
	"encoding/hex"
	"net/http"

	"github.com/antexprotocol/supernova/api/utils"
	"github.com/antexprotocol/supernova/chain"
	"github.com/antexprotocol/supernova/consensus"
	"github.com/antexprotocol/supernova/libs/p2p"
	"github.com/gorilla/mux"
)

type Node struct {
	version   string
	chainId   uint64
	p2pSrv    p2p.P2P
	Pacemaker *consensus.Pacemaker
	Chain     *chain.Chain
	pubkey    string
}

func New(version string, chainId uint64, p2pSrv p2p.P2P, pacemaker *consensus.Pacemaker, c *chain.Chain, pubkey []byte) *Node {
	return &Node{
		version,
		chainId,
		p2pSrv,
		pacemaker,
		c,
		hex.EncodeToString(pubkey),
	}
}

func (n *Node) handleChainId(w http.ResponseWriter, req *http.Request) error {
	return utils.WriteJSON(w, n.chainId) // mainnet

}

func (n *Node) handleVersion(w http.ResponseWriter, r *http.Request) error {
	return utils.WriteJSON(w, n.version)
}

func (n *Node) handleProbe(w http.ResponseWriter, r *http.Request) error {
	name := ""

	bestBlock, _ := convertBlock(n.Chain.BestBlock())
	bestQC, _ := convertQC(n.Chain.BestQC())
	pmProbe := n.Pacemaker.Probe()
	pacemaker, _ := convertPacemakerProbe(pmProbe)
	chainProbe := &ChainProbe{
		BestBlock: bestBlock,
		BestQC:    bestQC,
	}
	result := ProbeResult{
		Name:           name,
		PubKey:         n.pubkey,
		Version:        n.version,
		InCommittee:    pmProbe.InCommittee,
		CommitteeSize:  uint32(pmProbe.CommitteeSize),
		CommitteeIndex: uint32(pmProbe.CommitteeIndex),

		BestQC:    bestQC.Round, // n.Chain.BestBlock().Number(),
		BestBlock: bestBlock.Number,
		Pacemaker: pacemaker,
		Chain:     chainProbe,
	}

	return utils.WriteJSON(w, result)
}

func (n *Node) Mount(root *mux.Router, pathPrefix string) {
	sub := root.PathPrefix(pathPrefix).Subrouter()

	sub.Path("/chainid").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleChainId))
	sub.Path("/version").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleVersion))
	sub.Path("/probe").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleProbe))

}
