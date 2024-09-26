// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package node

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/meterio/meter-pov/block"
	"github.com/meterio/meter-pov/meter"
)

type blockStats struct {
	exec, commit               mclock.AbsTime
	txs                        int
	usedGas                    uint64
	processed, queued, ignored int
}

func (s *blockStats) UpdateProcessed(n int, txs int, exec, commit mclock.AbsTime, usedGas uint64) {
	s.processed += n
	s.txs += txs
	s.exec += exec
	s.commit += commit
	s.usedGas += usedGas
}

func (s *blockStats) UpdateIgnored(n int) {
	s.ignored += n
}

func (s *blockStats) UpdateQueued(n int) {
	s.queued += n
}

func (s *blockStats) LogContext(last *block.Header, pending int) []interface{} {
	return []interface{}{
		"txs", s.txs,
		"pending", pending,
		"blks/s", float64(s.processed) * 1000 * 1000 * 1000 / (float64(s.exec) + float64(s.commit)),
		"mgas", float64(s.usedGas) / 1000 / 1000,
		"et", fmt.Sprintf("%v|%v", meter.PrettyDuration(s.exec), meter.PrettyDuration(s.commit)),
		"mgas/s", float64(s.usedGas) * 1000 / float64(s.exec+s.commit),
		"id", last.ID().ToBlockShortID(),
	}
}
