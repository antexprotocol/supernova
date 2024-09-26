// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package api

import (
	"net/http"
	"strings"

	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/meterio/meter-pov/api/accountlock"
	"github.com/meterio/meter-pov/api/accounts"
	"github.com/meterio/meter-pov/api/auction"
	"github.com/meterio/meter-pov/api/blocks"
	"github.com/meterio/meter-pov/api/debug"
	"github.com/meterio/meter-pov/api/doc"
	"github.com/meterio/meter-pov/api/node"
	"github.com/meterio/meter-pov/api/peers"
	"github.com/meterio/meter-pov/api/slashing"
	"github.com/meterio/meter-pov/api/staking"
	"github.com/meterio/meter-pov/api/transactions"
	"github.com/meterio/meter-pov/chain"
	"github.com/meterio/meter-pov/consensus"
	"github.com/meterio/meter-pov/p2psrv"
	"github.com/meterio/meter-pov/state"
	"github.com/meterio/meter-pov/txpool"
)

// New return api router
func New(reactor *consensus.Reactor, chain *chain.Chain, stateCreator *state.Creator, txPool *txpool.TxPool, nw node.Network, allowedOrigins string, backtraceLimit uint32, callGasLimit uint64, p2pServer *p2psrv.Server, pubKey string) (http.HandlerFunc, func()) {
	origins := strings.Split(strings.TrimSpace(allowedOrigins), ",")
	for i, o := range origins {
		origins[i] = strings.ToLower(strings.TrimSpace(o))
	}

	router := mux.NewRouter()

	// to serve api doc and swagger-ui
	router.PathPrefix("/doc").Handler(
		http.StripPrefix("/doc/", http.FileServer(
			&assetfs.AssetFS{
				Asset:     doc.Asset,
				AssetDir:  doc.AssetDir,
				AssetInfo: doc.AssetInfo})))

	// redirect swagger-ui
	router.Path("/").HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "doc/swagger-ui/", http.StatusTemporaryRedirect)
		})

	accounts.New(chain, stateCreator, callGasLimit).
		Mount(router, "/accounts")

	blocks.New(chain, stateCreator).
		Mount(router, "/blocks")
	transactions.New(chain, stateCreator, txPool).
		Mount(router, "/transactions")
	debug.New(chain, stateCreator).
		Mount(router, "/debug")
	node.New(nw, reactor, pubKey).
		Mount(router, "/node")
	peers.New(p2pServer).Mount(router, "/peers")
	staking.New(chain, stateCreator).
		Mount(router, "/staking")
	slashing.New(chain, stateCreator).
		Mount(router, "/slashing")
	auction.New(chain, stateCreator).
		Mount(router, "/auction")
	accountlock.New(chain, stateCreator).
		Mount(router, "/accountlock")

	return handlers.CORS(
			handlers.AllowedOrigins(origins),
			handlers.AllowedHeaders([]string{"content-type"}))(router).ServeHTTP,
		func() {} // subscriptions handles hijacked conns, which need to be closed
}
