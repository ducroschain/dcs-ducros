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
	"fmt"
	"math/big"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"golang.org/x/crypto/sha3"
)

// RandomX proof-of-work protocol constants.
var (
	FrontierBlockReward           = uint256.NewInt(5e+18) // Block reward in wei for successfully mining a block
	ByzantiumBlockReward          = uint256.NewInt(3e+18) // Block reward in wei for successfully mining a block upward from Byzantium
	ConstantinopleBlockReward     = uint256.NewInt(2e+18) // Block reward in wei for successfully mining a block upward from Constantinople
	maxUncles                     = 2                     // Maximum number of uncles allowed in a single block
	allowedFutureBlockTimeSeconds = int64(15)             // Max seconds from current time allowed for blocks, before they're considered future blocks

	// Treasury system: 5% of all block rewards accumulate in treasury
	// Every Sunday at midnight UTC, the entire treasury balance is transferred to the owner address
	//
	// IMPORTANT: Change these addresses before production deployment!
	TreasuryAccumulationAddress = common.HexToAddress("0x4a0508d3a953882fd9b4859a5cf08d56dd32ee1b") // Treasury accumulation address - Infrastructure
	TreasuryOwnerAddress        = common.HexToAddress("0xe249b5c46b43e6796c2f26999a2c00d47dc49d1f") // My personal wallet - Alexandre Ducros
	TreasuryPercentage          = uint64(5)                                                         // 5% of rewards go to treasury

	// calcDifficultyEip5133 is the difficulty adjustment algorithm as specified by EIP 5133.
	// It offsets the bomb a total of 11.4M blocks.
	// Specification EIP-5133: https://eips.ethereum.org/EIPS/eip-5133
	calcDifficultyEip5133 = makeDifficultyCalculator(big.NewInt(11_400_000))

	// calcDifficultyEip4345 is the difficulty adjustment algorithm as specified by EIP 4345.
	// It offsets the bomb a total of 10.7M blocks.
	// Specification EIP-4345: https://eips.ethereum.org/EIPS/eip-4345
	calcDifficultyEip4345 = makeDifficultyCalculator(big.NewInt(10_700_000))

	// calcDifficultyEip3554 is the difficulty adjustment algorithm as specified by EIP 3554.
	// It offsets the bomb a total of 9.7M blocks.
	// Specification EIP-3554: https://eips.ethereum.org/EIPS/eip-3554
	calcDifficultyEip3554 = makeDifficultyCalculator(big.NewInt(9700000))

	// calcDifficultyEip2384 is the difficulty adjustment algorithm as specified by EIP 2384.
	// It offsets the bomb 4M blocks from Constantinople, so in total 9M blocks.
	// Specification EIP-2384: https://eips.ethereum.org/EIPS/eip-2384
	calcDifficultyEip2384 = makeDifficultyCalculator(big.NewInt(9000000))

	// calcDifficultyConstantinople is the difficulty adjustment algorithm for Constantinople.
	// It returns the difficulty that a new block should have when created at time given the
	// parent block's time and difficulty. The calculation uses the Byzantium rules, but with
	// bomb offset 5M.
	// Specification EIP-1234: https://eips.ethereum.org/EIPS/eip-1234
	calcDifficultyConstantinople = makeDifficultyCalculator(big.NewInt(5000000))

	// calcDifficultyByzantium is the difficulty adjustment algorithm. It returns
	// the difficulty that a new block should have when created at time given the
	// parent block's time and difficulty. The calculation uses the Byzantium rules.
	// Specification EIP-649: https://eips.ethereum.org/EIPS/eip-649
	calcDifficultyByzantium = makeDifficultyCalculator(big.NewInt(3000000))
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	errOlderBlockTime  = errors.New("timestamp older than parent")
	errTooManyUncles   = errors.New("too many uncles")
	errDuplicateUncle  = errors.New("duplicate uncle")
	errUncleIsAncestor = errors.New("uncle is ancestor")
	errDanglingUncle   = errors.New("uncle's parent is not ancestor")
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (randomx *RandomX) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the
// stock Ethereum RandomX engine.
func (randomx *RandomX) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	// Short circuit if the header is known, or its parent not
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return randomx.verifyHeader(chain, header, parent, false, time.Now().Unix())
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (randomx *RandomX) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if randomx.fakeFull || len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}
	abort := make(chan struct{})
	results := make(chan error, len(headers))
	unixNow := time.Now().Unix()

	go func() {
		for i, header := range headers {
			var parent *types.Header
			if i == 0 {
				parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
			} else if headers[i-1].Hash() == headers[i].ParentHash {
				parent = headers[i-1]
			}
			var err error
			if parent == nil {
				err = consensus.ErrUnknownAncestor
			} else {
				err = randomx.verifyHeader(chain, header, parent, false, unixNow)
			}
			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

// VerifyUncles verifies that the given block's uncles conform to the consensus
// rules of the stock Ethereum RandomX engine.
func (randomx *RandomX) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	// If we're running a full engine faking, accept any input as valid
	if randomx.fakeFull {
		return nil
	}
	// Verify that there are at most 2 uncles included in this block
	if len(block.Uncles()) > maxUncles {
		return errTooManyUncles
	}
	if len(block.Uncles()) == 0 {
		return nil
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := mapset.NewSet[common.Hash](), make(map[common.Hash]*types.Header)

	number, parent := block.NumberU64()-1, block.ParentHash()
	for i := 0; i < 7; i++ {
		ancestorHeader := chain.GetHeader(parent, number)
		if ancestorHeader == nil {
			break
		}
		ancestors[parent] = ancestorHeader
		// If the ancestor doesn't have any uncles, we don't have to iterate them
		if ancestorHeader.UncleHash != types.EmptyUncleHash {
			// Need to add those uncles to the banned list too
			ancestor := chain.GetBlock(parent, number)
			if ancestor == nil {
				break
			}
			for _, uncle := range ancestor.Uncles() {
				uncles.Add(uncle.Hash())
			}
		}
		parent, number = ancestorHeader.ParentHash, number-1
	}
	ancestors[block.Hash()] = block.Header()
	uncles.Add(block.Hash())

	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles.Contains(hash) {
			return errDuplicateUncle
		}
		uncles.Add(hash)

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return errUncleIsAncestor
		}
		if ancestors[uncle.ParentHash] == nil || uncle.ParentHash == block.ParentHash() {
			return errDanglingUncle
		}
		if err := randomx.verifyHeader(chain, uncle, ancestors[uncle.ParentHash], true, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules of the
// stock Ethereum RandomX engine.
// See YP section 4.3.4. "Block Header Validity"
func (randomx *RandomX) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.Header, uncle bool, unixNow int64) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if !uncle {
		if header.Time > uint64(unixNow+allowedFutureBlockTimeSeconds) {
			return consensus.ErrFutureBlock
		}
	}
	// Allow equal timestamp but enforce median-time-past (MTP) rule to prevent manipulation
	// For LWMA difficulty algorithm, we need to prevent timestamp manipulation
	// Allow same timestamp as parent, but verify against median of last 11 blocks
	if header.Time < parent.Time {
		return errOlderBlockTime
	}
	// Verify median-time-past: block timestamp must be greater than median of last 11 blocks
	if err := randomx.verifyMedianTimePast(chain, header, parent); err != nil {
		return err
	}
	// Verify the block's difficulty based on its timestamp and parent's difficulty
	expected := randomx.CalcDifficulty(chain, header.Time, parent)

	if expected.Cmp(header.Difficulty) != 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty, expected)
	}
	// Verify that the gas limit is <= 2^63-1
	if header.GasLimit > params.MaxGasLimit {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, params.MaxGasLimit)
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	// Verify the block's gas usage and (if applicable) verify the base fee.
	if !chain.Config().IsLondon(header.Number) {
		// Verify BaseFee not present before EIP-1559 fork.
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, expected 'nil'", header.BaseFee)
		}
		if err := misc.VerifyGaslimit(parent.GasLimit, header.GasLimit); err != nil {
			return err
		}
	} else if err := eip1559.VerifyEIP1559Header(chain.Config(), parent, header); err != nil {
		// Verify the header's EIP-1559 attributes.
		return err
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	if chain.Config().IsShanghai(header.Number, header.Time) {
		return errors.New("randomx does not support shanghai fork")
	}
	// Verify the non-existence of withdrawalsHash.
	if header.WithdrawalsHash != nil {
		return fmt.Errorf("invalid withdrawalsHash: have %x, expected nil", header.WithdrawalsHash)
	}
	if chain.Config().IsCancun(header.Number, header.Time) {
		return errors.New("randomx does not support cancun fork")
	}
	// Verify the non-existence of cancun-specific header fields
	switch {
	case header.ExcessBlobGas != nil:
		return fmt.Errorf("invalid excessBlobGas: have %d, expected nil", header.ExcessBlobGas)
	case header.BlobGasUsed != nil:
		return fmt.Errorf("invalid blobGasUsed: have %d, expected nil", header.BlobGasUsed)
	case header.ParentBeaconRoot != nil:
		return fmt.Errorf("invalid parentBeaconRoot, have %#x, expected nil", header.ParentBeaconRoot)
	}
	// Add some fake checks for tests
	if randomx.fakeDelay != nil {
		time.Sleep(*randomx.fakeDelay)
	}
	if randomx.fakeFail != nil && *randomx.fakeFail == header.Number.Uint64() {
		return errors.New("invalid tester pow")
	}
	// Verify the RandomX proof-of-work (skip in fake/test modes)
	if !randomx.fakeFull && (randomx.config == nil || randomx.config.PowMode == ModeNormal) {
		if err := randomx.verifyPoW(chain, header); err != nil {
			return err
		}
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyDAOHeaderExtraData(chain.Config(), header); err != nil {
		return err
	}
	return nil
}

// verifyMedianTimePast implements the Median-Time-Past (MTP) rule to prevent timestamp manipulation
// The block timestamp must be greater than the median of the last 11 blocks
// This is critical for LWMA difficulty algorithm security
func (randomx *RandomX) verifyMedianTimePast(chain consensus.ChainHeaderReader, header, parent *types.Header) error {
	const medianTimeBlocks = 11

	// Collect timestamps from last 11 blocks
	timestamps := make([]uint64, 0, medianTimeBlocks)
	current := parent

	for i := 0; i < medianTimeBlocks && current != nil; i++ {
		timestamps = append(timestamps, current.Time)
		if current.Number.Uint64() == 0 {
			break
		}
		current = chain.GetHeader(current.ParentHash, current.Number.Uint64()-1)
	}

	// If we have less than 11 blocks, use what we have (early chain)
	if len(timestamps) == 0 {
		return nil
	}

	// Sort timestamps to find median
	sortedTimes := make([]uint64, len(timestamps))
	copy(sortedTimes, timestamps)
	for i := 0; i < len(sortedTimes)-1; i++ {
		for j := i + 1; j < len(sortedTimes); j++ {
			if sortedTimes[i] > sortedTimes[j] {
				sortedTimes[i], sortedTimes[j] = sortedTimes[j], sortedTimes[i]
			}
		}
	}

	// Get median (middle element)
	median := sortedTimes[len(sortedTimes)/2]

	// Block timestamp must be greater than median
	if header.Time <= median {
		return fmt.Errorf("timestamp %d not greater than median-time-past %d", header.Time, median)
	}

	return nil
}

// verifyPoW verifies the RandomX proof-of-work for a sealed header
func (randomx *RandomX) verifyPoW(chain consensus.ChainHeaderReader, header *types.Header) error {
	blockHash := header.Hash()

	// DoS protection: Check if we've recently verified this block
	randomx.verifyMutex.Lock()
	if randomx.recentBlocks.Contains(blockHash) {
		randomx.verifyMutex.Unlock()
		return nil // Already verified successfully
	}

	// Check fail cache to avoid re-verifying known bad blocks
	if err, exists := randomx.failCache.Get(blockHash); exists {
		randomx.verifyMutex.Unlock()
		return err.(error) // Return cached error
	}
	randomx.verifyMutex.Unlock()

	// Calculate the RandomX seed for this block's epoch
	// This uses the epoch-based system (2048 blocks) for cache stability
	seedHash, err := randomx.GetSeedHash(chain, header.Number)
	if err != nil {
		return fmt.Errorf("failed to calculate RandomX seed: %w", err)
	}

	// Initialize RandomX cache with the epoch seed
	// Cache is reused for all blocks in the same epoch (2048 blocks)
	if err := randomx.initCache(seedHash); err != nil {
		return fmt.Errorf("failed to initialize RandomX cache: %w", err)
	}

	// Get seal hash (before locking, as it doesn't need cache)
	sealHash := randomx.SealHash(header)

	// CRITICAL: Hold read lock for entire verification to prevent cache rotation
	// race condition where another thread could free the cache while we're using it
	randomx.cacheMutex.RLock()
	defer randomx.cacheMutex.RUnlock()

	// Verify cache key matches expected seed (prevent race where another thread
	// rotated cache to different epoch between initCache() and RLock acquisition)
	if randomx.cacheKey != seedHash {
		randomx.cacheMutex.RUnlock()
		// Cache was rotated by another thread, retry initialization
		if err := randomx.initCache(seedHash); err != nil {
			return fmt.Errorf("failed to re-initialize RandomX cache: %w", err)
		}
		randomx.cacheMutex.RLock()
		// Check again after re-init
		if randomx.cacheKey != seedHash {
			return fmt.Errorf("cache key mismatch after retry for block %d", header.Number)
		}
	}

	cache := randomx.cache
	if cache == nil {
		return fmt.Errorf("randomx cache not initialized for block %d", header.Number)
	}

	// Verify PoW using the cache (all C operations are in randomx.go)
	// Cache is protected by RLock for entire duration
	dataset := randomx.datasetReadyLocked()
	if err := verifyPoWWithCache(cache, dataset, sealHash, header); err != nil {
		verifyErr := fmt.Errorf("proof-of-work verification failed: %w", err)
		// Cache the failure to prevent re-verification attacks
		randomx.verifyMutex.Lock()
		randomx.failCache.Add(blockHash, verifyErr)
		randomx.verifyMutex.Unlock()
		return verifyErr
	}

	// Cache successful verification
	randomx.verifyMutex.Lock()
	randomx.recentBlocks.Add(blockHash, true)
	randomx.verifyMutex.Unlock()

	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func (randomx *RandomX) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	next := new(big.Int).Add(parent.Number, big1)

	// Check if LWMA should be used for this block
	if ShouldUseLWMA(chain.Config(), next) {
		// Use LWMA difficulty algorithm (optimized for CPU mining)
		return CalcDifficultyLWMA(chain, time, parent)
	}

	// Fallback to Ethereum difficulty algorithms
	return CalcDifficulty(chain.Config(), time, parent)
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func CalcDifficulty(config *params.ChainConfig, time uint64, parent *types.Header) *big.Int {
	next := new(big.Int).Add(parent.Number, big1)

	// PRIORITY: Use LWMA for RandomX chains if enabled
	// LWMA (Linearly Weighted Moving Average) is specifically designed for
	// CPU-minable chains and handles hashrate variance much better than
	// Ethereum's original difficulty algorithms
	if ShouldUseLWMA(config, next) {
		// Note: This requires chain reader, so we need to modify the signature
		// For now, return a note that LWMA needs chain context
		// In practice, this is called from randomx.CalcDifficulty which has chain access
		return calcDifficultyFrontier(time, parent) // Fallback, will be replaced by LWMA call
	}

	// Fallback to Ethereum difficulty algorithms for non-RandomX chains
	// or before LWMA activation block
	switch {
	case config.IsGrayGlacier(next):
		return calcDifficultyEip5133(time, parent)
	case config.IsArrowGlacier(next):
		return calcDifficultyEip4345(time, parent)
	case config.IsLondon(next):
		return calcDifficultyEip3554(time, parent)
	case config.IsMuirGlacier(next):
		return calcDifficultyEip2384(time, parent)
	case config.IsConstantinople(next):
		return calcDifficultyConstantinople(time, parent)
	case config.IsByzantium(next):
		return calcDifficultyByzantium(time, parent)
	case config.IsHomestead(next):
		return calcDifficultyHomestead(time, parent)
	default:
		return calcDifficultyFrontier(time, parent)
	}
}

// Some weird constants to avoid constant memory allocs for them.
var (
	expDiffPeriod = big.NewInt(100000)
	big1          = big.NewInt(1)
	big2          = big.NewInt(2)
	big9          = big.NewInt(9)
	big10         = big.NewInt(10)
	bigMinus99    = big.NewInt(-99)
)

// makeDifficultyCalculator creates a difficultyCalculator with the given bomb-delay.
// the difficulty is calculated with Byzantium rules, which differs from Homestead in
// how uncles affect the calculation
func makeDifficultyCalculator(bombDelay *big.Int) func(time uint64, parent *types.Header) *big.Int {
	// Note, the calculations below looks at the parent number, which is 1 below
	// the block number. Thus we remove one from the delay given
	bombDelayFromParent := new(big.Int).Sub(bombDelay, big1)
	return func(time uint64, parent *types.Header) *big.Int {
		// https://github.com/ethereum/EIPs/issues/100.
		// algorithm:
		// diff = (parent_diff +
		//         (parent_diff / 2048 * max((2 if len(parent.uncles) else 1) - ((timestamp - parent.timestamp) // 9), -99))
		//        ) + 2^(periodCount - 2)

		bigTime := new(big.Int).SetUint64(time)
		bigParentTime := new(big.Int).SetUint64(parent.Time)

		// holds intermediate values to make the algo easier to read & audit
		x := new(big.Int)
		y := new(big.Int)

		// (2 if len(parent_uncles) else 1) - (block_timestamp - parent_timestamp) // 9
		x.Sub(bigTime, bigParentTime)
		x.Div(x, big9)
		if parent.UncleHash == types.EmptyUncleHash {
			x.Sub(big1, x)
		} else {
			x.Sub(big2, x)
		}
		// max((2 if len(parent_uncles) else 1) - (block_timestamp - parent_timestamp) // 9, -99)
		if x.Cmp(bigMinus99) < 0 {
			x.Set(bigMinus99)
		}
		// parent_diff + (parent_diff / 2048 * max((2 if len(parent.uncles) else 1) - ((timestamp - parent.timestamp) // 9), -99))
		y.Div(parent.Difficulty, params.DifficultyBoundDivisor)
		x.Mul(y, x)
		x.Add(parent.Difficulty, x)

		// minimum difficulty can ever be (before exponential factor)
		if x.Cmp(params.MinimumDifficulty) < 0 {
			x.Set(params.MinimumDifficulty)
		}
		// calculate a fake block number for the ice-age delay
		// Specification: https://eips.ethereum.org/EIPS/eip-1234
		fakeBlockNumber := new(big.Int)
		if parent.Number.Cmp(bombDelayFromParent) >= 0 {
			fakeBlockNumber = fakeBlockNumber.Sub(parent.Number, bombDelayFromParent)
		}
		// for the exponential factor
		periodCount := fakeBlockNumber
		periodCount.Div(periodCount, expDiffPeriod)

		// the exponential factor, commonly referred to as "the bomb"
		// diff = diff + 2^(periodCount - 2)
		if periodCount.Cmp(big1) > 0 {
			y.Sub(periodCount, big2)
			y.Exp(big2, y, nil)
			x.Add(x, y)
		}
		return x
	}
}

// calcDifficultyHomestead is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time given the
// parent block's time and difficulty. The calculation uses the Homestead rules.
func calcDifficultyHomestead(time uint64, parent *types.Header) *big.Int {
	// https://github.com/ethereum/EIPs/blob/master/EIPS/eip-2.md
	// algorithm:
	// diff = (parent_diff +
	//         (parent_diff / 2048 * max(1 - (block_timestamp - parent_timestamp) // 10, -99))
	//        ) + 2^(periodCount - 2)

	bigTime := new(big.Int).SetUint64(time)
	bigParentTime := new(big.Int).SetUint64(parent.Time)

	// holds intermediate values to make the algo easier to read & audit
	x := new(big.Int)
	y := new(big.Int)

	// 1 - (block_timestamp - parent_timestamp) // 10
	x.Sub(bigTime, bigParentTime)
	x.Div(x, big10)
	x.Sub(big1, x)

	// max(1 - (block_timestamp - parent_timestamp) // 10, -99)
	if x.Cmp(bigMinus99) < 0 {
		x.Set(bigMinus99)
	}
	// (parent_diff + parent_diff // 2048 * max(1 - (block_timestamp - parent_timestamp) // 10, -99))
	y.Div(parent.Difficulty, params.DifficultyBoundDivisor)
	x.Mul(y, x)
	x.Add(parent.Difficulty, x)

	// minimum difficulty can ever be (before exponential factor)
	if x.Cmp(params.MinimumDifficulty) < 0 {
		x.Set(params.MinimumDifficulty)
	}
	// for the exponential factor
	periodCount := new(big.Int).Add(parent.Number, big1)
	periodCount.Div(periodCount, expDiffPeriod)

	// the exponential factor, commonly referred to as "the bomb"
	// diff = diff + 2^(periodCount - 2)
	if periodCount.Cmp(big1) > 0 {
		y.Sub(periodCount, big2)
		y.Exp(big2, y, nil)
		x.Add(x, y)
	}
	return x
}

// calcDifficultyFrontier is the difficulty adjustment algorithm. It returns the
// difficulty that a new block should have when created at time given the parent
// block's time and difficulty. The calculation uses the Frontier rules.
func calcDifficultyFrontier(time uint64, parent *types.Header) *big.Int {
	diff := new(big.Int)
	adjust := new(big.Int).Div(parent.Difficulty, params.DifficultyBoundDivisor)
	bigTime := new(big.Int)
	bigParentTime := new(big.Int)

	bigTime.SetUint64(time)
	bigParentTime.SetUint64(parent.Time)

	if bigTime.Sub(bigTime, bigParentTime).Cmp(params.DurationLimit) < 0 {
		diff.Add(parent.Difficulty, adjust)
	} else {
		diff.Sub(parent.Difficulty, adjust)
	}
	if diff.Cmp(params.MinimumDifficulty) < 0 {
		diff.Set(params.MinimumDifficulty)
	}

	periodCount := new(big.Int).Add(parent.Number, big1)
	periodCount.Div(periodCount, expDiffPeriod)
	if periodCount.Cmp(big1) > 0 {
		// diff = diff + 2^(periodCount - 2)
		expDiff := periodCount.Sub(periodCount, big2)
		expDiff.Exp(big2, expDiff, nil)
		diff.Add(diff, expDiff)
		if diff.Cmp(params.MinimumDifficulty) < 0 {
			diff = params.MinimumDifficulty
		}
	}
	return diff
}

// Exported for fuzzing
var FrontierDifficultyCalculator = calcDifficultyFrontier
var HomesteadDifficultyCalculator = calcDifficultyHomestead
var DynamicDifficultyCalculator = makeDifficultyCalculator

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the RandomX protocol. The changes are done inline.
func (randomx *RandomX) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty = randomx.CalcDifficulty(chain, header.Time, parent)
	return nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards.
func (randomx *RandomX) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state vm.StateDB, body *types.Body) {
	// Check if we need to transfer treasury (every Sunday)
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent != nil {
		transferTreasuryIfSunday(state, header, parent)
	}

	// Accumulate any block and uncle rewards
	accumulateRewards(chain.Config(), state, header, body.Uncles)
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (randomx *RandomX) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, body *types.Body, receipts []*types.Receipt) (*types.Block, error) {
	if len(body.Withdrawals) > 0 {
		return nil, errors.New("randomx does not support withdrawals")
	}
	// Finalize block
	randomx.Finalize(chain, header, state, body)

	// Assign the final state root to header.
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, &types.Body{Transactions: body.Transactions, Uncles: body.Uncles}, receipts, trie.NewStackTrie(nil)), nil
}

// SealHash returns the hash of a block prior to it being sealed.
func (randomx *RandomX) SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	// RandomX ignores PoS/blob-related fields - they should not be set for PoW blocks
	// But we don't panic to allow genesis configurations with Cancun enabled
	// These fields will simply be ignored in the seal hash calculation
	if header.WithdrawalsHash != nil {
		// Ignore withdrawals for RandomX PoW
	}
	if header.ExcessBlobGas != nil {
		// Ignore excess blob gas for RandomX PoW
	}
	if header.BlobGasUsed != nil {
		// Ignore blob gas used for RandomX PoW
	}
	if header.ParentBeaconRoot != nil {
		// Ignore parent beacon root for RandomX PoW
	}
	rlp.Encode(hasher, enc)
	hasher.Sum(hash[:0])
	return hash
}

// accumulateRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
//
// Treasury system: 95% of rewards go to the miner, 5% go to the treasury address.
// transferTreasuryIfSunday checks if we're transitioning to Sunday and transfers
// the entire treasury balance to the owner address
//
// This function ensures that treasury rewards are distributed weekly:
//   - During the week: 5% rewards accumulate in TreasuryAccumulationAddress
//   - Every Sunday: When we transition from Saturday->Sunday, ALL accumulated
//     treasury funds are transferred to TreasuryOwnerAddress (your personal wallet)
//
// The transfer happens exactly once per week by detecting the day transition.
func transferTreasuryIfSunday(stateDB vm.StateDB, header *types.Header, parent *types.Header) {
	// Convert block timestamps to UTC time
	blockTime := time.Unix(int64(header.Time), 0).UTC()
	parentTime := time.Unix(int64(parent.Time), 0).UTC()

	// Get weekdays
	blockDay := blockTime.Weekday()
	parentDay := parentTime.Weekday()

	// Transfer treasury when transitioning TO Sunday FROM any other day
	// This ensures exactly one transfer per week
	if blockDay == time.Sunday && parentDay != time.Sunday {
		// Get current treasury balance
		treasuryBalance := stateDB.GetBalance(TreasuryAccumulationAddress)

		// Only transfer if there's actually something to transfer
		if treasuryBalance.Sign() > 0 {
			// Move entire balance from treasury to owner
			stateDB.SubBalance(TreasuryAccumulationAddress, treasuryBalance, tracing.BalanceChangeTransfer)
			stateDB.AddBalance(TreasuryOwnerAddress, treasuryBalance, tracing.BalanceChangeTransfer)
		}
	}
}

func accumulateRewards(config *params.ChainConfig, stateDB vm.StateDB, header *types.Header, uncles []*types.Header) {
	// Select the correct block reward based on chain progression
	blockReward := FrontierBlockReward
	if config.IsByzantium(header.Number) {
		blockReward = ByzantiumBlockReward
	}
	if config.IsConstantinople(header.Number) {
		blockReward = ConstantinopleBlockReward
	}
	// Accumulate the rewards for the miner and any included uncles
	reward := new(uint256.Int).Set(blockReward)
	r := new(uint256.Int)
	hNum, _ := uint256.FromBig(header.Number)
	for _, uncle := range uncles {
		uNum, _ := uint256.FromBig(uncle.Number)
		r.AddUint64(uNum, 8)
		r.Sub(r, hNum)
		r.Mul(r, blockReward)
		r.Rsh(r, 3)
		stateDB.AddBalance(uncle.Coinbase, r, tracing.BalanceIncreaseRewardMineUncle)

		r.Rsh(blockReward, 5)
		reward.Add(reward, r)
	}

	// Anti-Botnet Protection: Check if miner is blacklisted
	// If blacklisted → 100% rewards go to treasury (miner gets nothing)
	// If normal → 95% to miner, 5% to treasury
	//
	// This protects the network from:
	// - Botnet mining operations
	// - Malware-infected computers mining without owner consent
	// - Stolen computing power
	// - Known criminal mining operations
	//
	// Note: Blacklist ONLY affects mining rewards, NOT regular transactions
	isBlacklisted := params.IsMinerBlacklisted(header.Coinbase)

	var minerReward, treasuryReward *uint256.Int

	if isBlacklisted {
		// Blacklisted miner: 100% to treasury, 0% to miner
		minerReward = uint256.NewInt(0)
		treasuryReward = new(uint256.Int).Set(reward)
	} else {
		// Normal miner: 95% to miner, 5% to treasury
		treasuryReward = new(uint256.Int).Set(reward)
		treasuryReward.Mul(treasuryReward, uint256.NewInt(TreasuryPercentage))
		treasuryReward.Div(treasuryReward, uint256.NewInt(100))

		minerReward = new(uint256.Int).Set(reward)
		minerReward.Sub(minerReward, treasuryReward)
	}

	// Distribute rewards
	stateDB.AddBalance(header.Coinbase, minerReward, tracing.BalanceIncreaseRewardMineBlock)
	stateDB.AddBalance(TreasuryAccumulationAddress, treasuryReward, tracing.BalanceIncreaseRewardMineBlock)
}
