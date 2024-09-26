// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package node

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/meterio/meter-pov/api/utils"
	"github.com/meterio/meter-pov/consensus"
	"github.com/meterio/meter-pov/meter"
	"github.com/meterio/meter-pov/powpool"
)

type Node struct {
	nw      Network
	reactor *consensus.Reactor
	pubKey  string
}

func New(nw Network, reactor *consensus.Reactor, pubKey string) *Node {
	return &Node{
		nw,
		reactor,
		pubKey,
	}
}

func (n *Node) PeersStats() []*PeerStats {
	return ConvertPeersStats(n.nw.PeersStats())
}

func (n *Node) handleNetwork(w http.ResponseWriter, req *http.Request) error {
	return utils.WriteJSON(w, n.PeersStats())
}

func (n *Node) handleCommittee(w http.ResponseWriter, req *http.Request) error {
	list, err := n.reactor.GetLatestCommitteeList()
	if err != nil {
		return err
	}
	committeeList := convertCommitteeList(list)
	return utils.WriteJSON(w, committeeList)
}

func (n *Node) handlePubKey(w http.ResponseWriter, req *http.Request) error {
	w.WriteHeader(http.StatusOK)
	utils.WriteJSON(w, n.pubKey)
	//	w.Write([]byte(b))
	return nil
}

func (n *Node) handleCoef(w http.ResponseWriter, req *http.Request) error {
	w.WriteHeader(http.StatusOK)
	pool := powpool.GetGlobPowPoolInst()
	utils.WriteJSON(w, pool.GetCurCoef())
	//	w.Write([]byte(b))
	return nil
}

func (n *Node) handleGetChainId(w http.ResponseWriter, req *http.Request) error {
	if meter.IsMainNet() {
		return utils.WriteJSON(w, 82) // mainnet
	}
	return utils.WriteJSON(w, 83) // testnet
}

func (n *Node) Mount(root *mux.Router, pathPrefix string) {
	sub := root.PathPrefix(pathPrefix).Subrouter()

	sub.Path("/network/peers").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleNetwork))
	sub.Path("/consensus/committee").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleCommittee))
	sub.Path("/pubkey").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handlePubKey))
	sub.Path("/coef").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleCoef))
	sub.Path("/chainid").Methods("Get").HandlerFunc(utils.WrapHandlerFunc(n.handleGetChainId))
}
