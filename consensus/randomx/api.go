//      .-') _                    (`-.   
//     ( OO ) )                 _(OO  )_ 
// ,--./ ,--,'  .-'),-----. ,--(_/   ,. \
// |   \ |  |\ ( OO'  .-.  '\   \   /(__/
// |    \|  | )/   |  | |  | \   \ /   / 
// |  .     |/ \_) |  |\|  |  \   '   /, 
// |  |\    |    \ |  | |  |   \     /__)
// |  | \   |     `'  '-'  '    \   /    
// `--'  `--'       `-----'      `-'     
//
//        ✧  N O V E M B R E   2 0 2 5  ✧
//
//        ✦  A J O U T   D U   F I C H I E R  ✦
//
//  Copyright © 2025 The go-ducros Authors
//  This file is part of the go-ducros library.
//
//  The go-ducros library is free software: you can redistribute it and/or modify
//  it under the terms of the GNU Lesser General Public License as published by
//  the Free Software Foundation, either version 3 of the License, or
//  (at your option) any later version.
//
//  The go-ducros library is distributed in the hope that it will be useful,
//  but WITHOUT ANY WARRANTY; without even the implied warranty of
//  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
//  GNU Lesser General Public License for more details.
//
//  You should have received a copy of the GNU Lesser General Public License
//  along with the go-ducros library. If not, see <http://www.gnu.org/licenses/>.


package randomx

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	errRandomXStopped    = errors.New("randomx stopped")
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// API exposes RandomX related methods for the RPC interface.
type API struct {
	randomx *RandomX
}

// GetWork returns a work package for external miner.
//
// The work package consists of 4 strings:
//
//	result[0] - 32 bytes hex encoded current block header pow-hash (SealHash)
//	result[1] - 32 bytes hex encoded seed hash (ParentHash) used for RandomX cache
//	result[2] - 32 bytes hex encoded boundary condition ("target"), 2^256/difficulty
//	result[3] - hex encoded block number
func (api *API) GetWork() ([4]string, error) {
	if api.randomx.remote == nil {
		return [4]string{}, errors.New("not supported")
	}

	var (
		workCh = make(chan [4]string, 1)
		errc   = make(chan error, 1)
	)

	select {
	case api.randomx.remote.fetchWorkCh <- &sealWork{errc: errc, res: workCh}:
	case <-api.randomx.remote.exitCh:
		return [4]string{}, errRandomXStopped
	}

	select {
	case work := <-workCh:
		return work, nil
	case err := <-errc:
		return [4]string{}, err
	}
}

// SubmitWork can be used by external miner to submit their POW solution.
// It returns an indication if the work was accepted.
// Note either an invalid solution, a stale work a non-existent work will return false.
func (api *API) SubmitWork(nonce types.BlockNonce, hash, digest common.Hash) bool {
	if api.randomx.remote == nil {
		return false
	}

	var errc = make(chan error, 1)

	select {
	case api.randomx.remote.submitWorkCh <- &mineResult{
		nonce:     nonce,
		mixDigest: digest,
		hash:      hash,
		errc:      errc,
	}:
	case <-api.randomx.remote.exitCh:
		return false
	}

	err := <-errc
	return err == nil
}

// SubmitHashrate can be used for remote miners to submit their hash rate.
// This enables the node to report the combined hash rate of all miners
// which submit work through this node.
//
// It accepts the miner hash rate and an identifier which must be unique
// between nodes.
func (api *API) SubmitHashrate(rate hexutil.Uint64, id common.Hash) bool {
	if api.randomx.remote == nil {
		return false
	}

	var done = make(chan struct{}, 1)

	select {
	case api.randomx.remote.submitRateCh <- &hashrate{done: done, rate: uint64(rate), id: id}:
	case <-api.randomx.remote.exitCh:
		return false
	}

	// Block until hash rate submitted successfully.
	<-done

	return true
}

// GetHashrate returns the current hashrate for local CPU miner and remote miner.
func (api *API) GetHashrate() uint64 {
	return uint64(api.randomx.Hashrate())
}

// Hashrate implements consensus.Engine, returning the measured rate of the search invocations
// per second over the last minute.
// Note the returned hashrate includes local hashrate, but also includes the total hashrate
// of all remote miner.
func (randomx *RandomX) Hashrate() float64 {
	// Short circuit if we are run the randomx in normal/test mode.
	if randomx.remote == nil {
		return randomx.hashrate.Snapshot().Rate1()
	}
	var res = make(chan uint64, 1)

	select {
	case randomx.remote.fetchRateCh <- res:
	case <-randomx.remote.exitCh:
		// Return local hashrate only if randomx is stopped.
		return randomx.hashrate.Snapshot().Rate1()
	}

	// Gather total submitted hash rate of remote sealers.
	return randomx.hashrate.Snapshot().Rate1() + float64(<-res)
}

// APIs returns the RPC APIs this consensus engine provides.
func (randomx *RandomX) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	// In order to ensure backward compatibility, we expose RandomX RPC APIs
	// to both eth and randomx namespaces.
	apis := []rpc.API{
		{
			Namespace: "eth",
			Service:   &API{randomx},
			Public:    true,
		},
		{
			Namespace: "randomx",
			Service:   &API{randomx},
			Public:    true,
		},
	}
	log.Info("RandomX APIs registered", "count", len(apis), "namespaces", []string{"eth", "randomx"}, "remote", randomx.remote != nil)
	return apis
}
