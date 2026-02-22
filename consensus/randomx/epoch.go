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
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/crypto"
)

// RandomX Epoch Configuration
// Following Monero's model for cache stability and miner compatibility
const (
	// EpochLength defines how many blocks before the RandomX seed changes.
	// Using 2048 blocks (~7 hours at 13s) like Monero for:
	// - Cache reuse optimization
	// - Miner compatibility (xmrig, etc.)
	// - Network stability
	EpochLength = 2048

	// EpochLag defines how many blocks behind we use for seed calculation.
	// Lag of 64 blocks (~13 minutes) provides:
	// - Protection against seed manipulation
	// - Time for network propagation
	// - Predictability for miners
	EpochLag = 64
)

// seedBlock returns the block number used for RandomX seed calculation
// at the given block height.
//
// Formula: seedBlock = (blockNumber - EpochLag) / EpochLength * EpochLength
//
// Examples:
//   - Block 0-2047: seed from block 0 (genesis)
//   - Block 2048-4095: seed from block 1984 (2048 - 64)
//   - Block 4096-6143: seed from block 4032 (4096 - 64)
//
// This ensures:
//   - Same seed for entire epoch (2048 blocks)
//   - Seed is 64 blocks old (lag protection)
//   - Miners can prepare cache in advance
func seedBlock(blockNumber uint64) uint64 {
	// Handle genesis and early blocks
	if blockNumber < EpochLag {
		return 0
	}

	// Calculate lagged block number
	laggedBlock := blockNumber - EpochLag

	// Round down to epoch boundary
	epochNumber := laggedBlock / EpochLength
	return epochNumber * EpochLength
}

// calcSeedHash returns the RandomX seed hash for the given block number.
// The seed is derived from the hash of the epoch's seed block.
//
// This function requires access to the chain to fetch historical block hashes.
func calcSeedHash(chain consensus.ChainHeaderReader, blockNumber *big.Int) (common.Hash, error) {
	if blockNumber == nil || blockNumber.Sign() <= 0 {
		// Genesis block or invalid - use zero hash
		return common.Hash{}, nil
	}

	num := blockNumber.Uint64()
	seedBlockNum := seedBlock(num)

	// For genesis or if seed block is genesis
	if seedBlockNum == 0 {
		// Use a fixed seed for genesis epoch
		// Hash of "DucrosRandomXGenesisSeed" for determinism
		return crypto.Keccak256Hash([]byte("DucrosRandomXGenesisSeed")), nil
	}

	// Get the seed block header
	// CRITICAL: This MUST be available or the node cannot validate blocks
	seedHeader := chain.GetHeaderByNumber(seedBlockNum)
	if seedHeader == nil {
		// CONSENSUS FAILURE: Seed block header not found
		// This indicates the node is not fully synced or has a corrupted chain
		// We CANNOT use a fallback as it would cause consensus divergence
		return common.Hash{}, fmt.Errorf("seed block %d header not found (consensus critical)", seedBlockNum)
	}

	// Use the block hash as RandomX seed
	// This is deterministic and known to all nodes
	return seedHeader.Hash(), nil
}

// GetSeedHash is the public API to get RandomX seed for a block.
// Used by both verification and mining.
func (randomx *RandomX) GetSeedHash(chain consensus.ChainHeaderReader, blockNumber *big.Int) (common.Hash, error) {
	// In fake modes, use a fixed seed for testing
	if randomx.fakeFull || randomx.fakeDelay != nil {
		return crypto.Keccak256Hash([]byte("FakeModeSeed")), nil
	}

	return calcSeedHash(chain, blockNumber)
}

// GetEpochNumber returns the epoch number for a given block.
// Epoch numbers are sequential: 0, 1, 2, ...
func GetEpochNumber(blockNumber uint64) uint64 {
	if blockNumber < EpochLag {
		return 0
	}
	return (blockNumber - EpochLag) / EpochLength
}

// IsEpochTransition returns true if the given block number is the first block
// of a new epoch (where seed changes).
func IsEpochTransition(blockNumber uint64) bool {
	if blockNumber < EpochLag {
		return blockNumber == 0
	}
	laggedBlock := blockNumber - EpochLag
	return laggedBlock%EpochLength == 0
}

// GetEpochTransitionBlock returns the block number where the next epoch starts.
func GetEpochTransitionBlock(currentBlock uint64) uint64 {
	if currentBlock < EpochLag {
		return EpochLag
	}
	laggedBlock := currentBlock - EpochLag
	currentEpoch := laggedBlock / EpochLength
	nextEpoch := currentEpoch + 1
	return (nextEpoch * EpochLength) + EpochLag
}
