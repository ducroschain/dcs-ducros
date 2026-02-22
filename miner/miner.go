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
//        ✦  F I C H I E R   M O D I F I É  ✦
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

// ============================================================================

// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package miner implements Ethereum block creation and mining.
package miner

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// Backend wraps all methods required for mining. Only full node is capable
// to offer all the functions here.
type Backend interface {
	BlockChain() *core.BlockChain
	TxPool() *txpool.TxPool
}

// Config is the configuration parameters of mining.
type Config struct {
	Etherbase           common.Address `toml:"-"`          // Deprecated
	PendingFeeRecipient common.Address `toml:"-"`          // Address for pending block rewards.
	ExtraData           hexutil.Bytes  `toml:",omitempty"` // Block extra data set by the miner
	GasCeil             uint64         // Target gas ceiling for mined blocks.
	GasPrice            *big.Int       // Minimum gas price for mining a transaction
	Recommit            time.Duration  // The time interval for miner to re-create mining work.
}

// DefaultConfig contains default settings for miner.
var DefaultConfig = Config{
	GasCeil:  60_000_000,
	GasPrice: big.NewInt(params.GWei / 1000),

	// The default recommit time is chosen as two seconds since
	// consensus-layer usually will wait a half slot of time(6s)
	// for payload generation. It should be enough for Geth to
	// run 3 rounds.
	Recommit: 2 * time.Second,
}

// Miner is the main object which takes care of submitting new work to consensus
// engine and gathering the sealing result.
type Miner struct {
	confMu      sync.RWMutex // The lock used to protect the config fields: GasCeil, GasTip and Extradata
	config      *Config
	chainConfig *params.ChainConfig
	engine      consensus.Engine
	txpool      *txpool.TxPool
	prio        []common.Address // A list of senders to prioritize
	chain       *core.BlockChain
	pending     *pending
	pendingMu   sync.Mutex // Lock protects the pending block

	// PoW mining fields
	mining    bool           // Mining status
	miningMu  sync.RWMutex   // Lock for mining status
	mineStop  chan struct{}  // Stop channel for mining
	threads   int            // Number of mining threads
}

// New creates a new miner with provided config.
func New(eth Backend, config Config, engine consensus.Engine) *Miner {
	return &Miner{
		config:      &config,
		chainConfig: eth.BlockChain().Config(),
		engine:      engine,
		txpool:      eth.TxPool(),
		chain:       eth.BlockChain(),
		pending:     &pending{},
	}
}

// Pending returns the currently pending block and associated receipts, logs
// and statedb. The returned values can be nil in case the pending block is
// not initialized.
func (miner *Miner) Pending() (*types.Block, types.Receipts, *state.StateDB) {
	pending := miner.getPending()
	if pending == nil {
		return nil, nil, nil
	}
	return pending.block, pending.receipts, pending.stateDB.Copy()
}

// SetExtra sets the content used to initialize the block extra field.
func (miner *Miner) SetExtra(extra []byte) error {
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra exceeds max length. %d > %v", len(extra), params.MaximumExtraDataSize)
	}
	miner.confMu.Lock()
	miner.config.ExtraData = extra
	miner.confMu.Unlock()
	return nil
}

// SetPrioAddresses sets a list of addresses to prioritize for transaction inclusion.
func (miner *Miner) SetPrioAddresses(prio []common.Address) {
	miner.confMu.Lock()
	miner.prio = prio
	miner.confMu.Unlock()
}

// SetGasCeil sets the gaslimit to strive for when mining blocks post 1559.
// For pre-1559 blocks, it sets the ceiling.
func (miner *Miner) SetGasCeil(ceil uint64) {
	miner.confMu.Lock()
	miner.config.GasCeil = ceil
	miner.confMu.Unlock()
}

// SetGasTip sets the minimum gas tip for inclusion.
func (miner *Miner) SetGasTip(tip *big.Int) error {
	miner.confMu.Lock()
	miner.config.GasPrice = tip
	miner.confMu.Unlock()
	return nil
}

// SetEtherbase sets the etherbase address for mining rewards.
// This is the address that will receive block rewards when mining.
func (miner *Miner) SetEtherbase(addr common.Address) {
	miner.confMu.Lock()
	miner.config.PendingFeeRecipient = addr
	miner.config.Etherbase = addr
	miner.confMu.Unlock()
	log.Info("Etherbase updated", "address", addr.Hex())
}

// GetEtherbase returns the current etherbase address for mining rewards.
func (miner *Miner) GetEtherbase() common.Address {
	miner.confMu.RLock()
	defer miner.confMu.RUnlock()
	if miner.config.PendingFeeRecipient != (common.Address{}) {
		return miner.config.PendingFeeRecipient
	}
	return miner.config.Etherbase
}

// BuildPayload builds the payload according to the provided parameters.
func (miner *Miner) BuildPayload(args *BuildPayloadArgs, witness bool) (*Payload, error) {
	return miner.buildPayload(args, witness)
}

// getPending retrieves the pending block based on the current head block.
// The result might be nil if pending generation is failed.
func (miner *Miner) getPending() *newPayloadResult {
	header := miner.chain.CurrentHeader()
	miner.pendingMu.Lock()
	defer miner.pendingMu.Unlock()

	if cached := miner.pending.resolve(header.Hash()); cached != nil {
		return cached
	}
	var (
		timestamp  = uint64(time.Now().Unix())
		withdrawal types.Withdrawals
	)
	if miner.chainConfig.IsShanghai(new(big.Int).Add(header.Number, big.NewInt(1)), timestamp) {
		withdrawal = []*types.Withdrawal{}
	}
	ret := miner.generateWork(&generateParams{
		timestamp:   timestamp,
		forceTime:   false,
		parentHash:  header.Hash(),
		coinbase:    miner.config.PendingFeeRecipient,
		random:      common.Hash{},
		withdrawals: withdrawal,
		beaconRoot:  nil,
		noTxs:       false,
	}, false) // we will never make a witness for a pending block
	if ret.err != nil {
		return nil
	}
	miner.pending.update(header.Hash(), ret)
	return ret
}

// Start starts the mining process with the given number of threads.
// This is for PoW consensus engines only.
func (miner *Miner) Start(threads int) error {
	miner.miningMu.Lock()
	defer miner.miningMu.Unlock()

	if miner.mining {
		return fmt.Errorf("mining is already running")
	}

	if threads <= 0 {
		threads = 1
	}

	miner.mining = true
	miner.threads = threads
	miner.mineStop = make(chan struct{})

	// Start mining goroutines
	for i := 0; i < threads; i++ {
		go miner.mineLoop(miner.mineStop)
	}

	return nil
}

// Stop stops the mining process.
func (miner *Miner) Stop() error {
	miner.miningMu.Lock()
	defer miner.miningMu.Unlock()

	if !miner.mining {
		return fmt.Errorf("mining is not running")
	}

	close(miner.mineStop)
	miner.mining = false
	return nil
}

// Mining returns whether the miner is currently mining.
func (miner *Miner) Mining() bool {
	miner.miningMu.RLock()
	defer miner.miningMu.RUnlock()
	return miner.mining
}

// HashRate returns the current hash rate (always returns 0 for now, can be improved later).
func (miner *Miner) HashRate() uint64 {
	// TODO: Implement actual hashrate calculation
	return 0
}

// mineLoop is the main mining loop that continuously tries to mine blocks.
func (miner *Miner) mineLoop(stop <-chan struct{}) {
	log.Info("Mining loop started")
	for {
		select {
		case <-stop:
			log.Info("Mining loop stopped")
			return
		default:
			// Get the current head block
			header := miner.chain.CurrentBlock()
			if header == nil {
				log.Warn("Current block header is nil, waiting...")
				time.Sleep(time.Second)
				continue
			}

			log.Info("Mining new block", "parent", header.Number.Uint64(), "difficulty", header.Difficulty)

			// Create a new block to mine
			timestamp := uint64(time.Now().Unix())
			parent := miner.chain.GetBlock(header.Hash(), header.Number.Uint64())
			if parent == nil {
				log.Warn("Parent block is nil, waiting...")
				time.Sleep(time.Second)
				continue
			}

			// Build a new block
			coinbase := miner.config.PendingFeeRecipient
			if coinbase == (common.Address{}) {
				coinbase = miner.config.Etherbase
			}

			log.Debug("Generating work", "coinbase", coinbase, "timestamp", timestamp)

			// Generate work
			result := miner.generateWork(&generateParams{
				timestamp:   timestamp,
				forceTime:   true,
				parentHash:  parent.Hash(),
				coinbase:    coinbase,
				random:      common.Hash{},
				withdrawals: nil,
				beaconRoot:  nil,
				noTxs:       false,
			}, false)

			if result.err != nil {
				log.Error("Failed to generate work", "err", result.err)
				time.Sleep(time.Second)
				continue
			}

			log.Info("Starting to seal block", "number", result.block.NumberU64(), "difficulty", result.block.Difficulty())

			// Try to seal the block using the consensus engine
			resultCh := make(chan *types.Block, 1)
			if err := miner.engine.Seal(miner.chain, result.block, resultCh, stop); err != nil {
				log.Error("Seal failed", "err", err)
				time.Sleep(time.Second)
				continue
			}

			// Wait for the sealing result
			select {
			case block := <-resultCh:
				if block != nil {
					log.Info("Block sealed successfully!", "number", block.NumberU64(), "hash", block.Hash().Hex())
					// Insert the sealed block into the blockchain
					_, err := miner.chain.InsertChain([]*types.Block{block})
					if err != nil {
						log.Error("Failed to insert block", "err", err)
						// Block insertion failed, continue mining
						continue
					}
					log.Info("🎉 Successfully mined block!", "number", block.NumberU64(), "hash", block.Hash().Hex())
				} else {
					log.Warn("Received nil block from seal")
				}
			case <-stop:
				log.Info("Mining stopped during sealing")
				return
			case <-time.After(30 * time.Second):
				log.Warn("Sealing timeout after 30 seconds")
				// Timeout, try again
				continue
			}
		}
	}
}
