// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/antexprotocol/supernova/api/blocks"
	"github.com/antexprotocol/supernova/api/doc"
	"github.com/antexprotocol/supernova/api/node"
	"github.com/antexprotocol/supernova/chain"
	"github.com/antexprotocol/supernova/consensus"
	"github.com/antexprotocol/supernova/libs/co"
	"github.com/antexprotocol/supernova/libs/p2p"
	"github.com/antexprotocol/supernova/txpool"
	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

type APIServer struct {
	listenAddr string
	handler    http.Handler
}

// New return api router
func NewAPIServer(listenAddr string, chainId uint64, version string, chain *chain.Chain, txPool *txpool.TxPool, pacemaker *consensus.Pacemaker, pubkey []byte, p2pSrv p2p.P2P) *APIServer {
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

	blocks.New(chain).
		Mount(router, "/blocks")
	node.New(version, chainId, p2pSrv, pacemaker, chain, pubkey).
		Mount(router, "/node")

	return &APIServer{
		listenAddr: listenAddr,
		handler: handlers.CORS(
			handlers.AllowedOrigins([]string{"*"}),
			handlers.AllowedHeaders([]string{"content-type"}))(router)}
}

func (api *APIServer) Start(ctx context.Context) {

	listener, err := net.Listen("tcp", api.listenAddr)
	if err != nil {
		panic(err)
	}

	timeout := 10000
	handler := api.handler
	handler = handleAPITimeout(handler, time.Duration(timeout)*time.Millisecond)
	handler = handleXVersion(handler)
	handler = requestBodyLimit(handler)
	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 18 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	var goes co.Goes
	goes.Go(func() {
		slog.Info("API started", "addr", api.listenAddr)
		err := srv.Serve(listener)
		if err != nil {
			slog.Warn(err.Error())
		}
	})
	<-ctx.Done()
	srv.Shutdown(ctx)
	goes.Wait()

}

// middleware to set 'x-meter-ver' to response headers.
func handleXVersion(h http.Handler) http.Handler {
	const headerKey = "x-version"
	ver := doc.Version()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerKey, ver)
		h.ServeHTTP(w, r)
	})
}

// middleware for http request timeout.
func handleAPITimeout(h http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		h.ServeHTTP(w, r)
	})
}

// middleware to limit request body size.
func requestBodyLimit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 96*1000)
		h.ServeHTTP(w, r)
	})
}
