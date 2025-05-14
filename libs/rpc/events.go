// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package rpc

import (
	"context"

	"github.com/meterio/supernova/block"
)

// NewBlockEvent event emitted when received block announcement.
type NewBlockEvent struct {
	*block.EscortedBlock
}

// HandleBlockStream to handle the stream of downloaded blocks in sync process.
type HandleBlockStream func(ctx context.Context, stream <-chan *block.EscortedBlock) error
