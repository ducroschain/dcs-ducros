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

// LWMA (Linearly Weighted Moving Average) Difficulty Algorithm
// Optimized for CPU-minable RandomX chains

package randomx

import (
	"math/big"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

const (
	LWMAWindowSize              = 60
	LWMATargetBlockTime         = 13
	LWMAMinDifficulty           = 3630     // Initial difficulty - will adjust after first blocks
	LWMAMaxAdjustmentUp         = 2       // 2× max increase per block (anti-pump)
	LWMAMaxAdjustmentDown       = 2       // 2× max decrease per block (anti-dump)
	LWMATimestampMaxFutureDrift = 15      // 15 seconds future tolerance
	LWMATimestampMaxPastDrift   = 91      // 91 seconds past tolerance (7× target time)
	LWMABurstDetectionWindow    = 10      // Last 10 blocks for burst detection
	LWMABurstThreshold          = 3       // 3× variance = burst attack suspected
	LWMADampingFactor           = 0.9     // Damping for rapid adjustments (90%)
)

// CalcDifficultyLWMA calculates difficulty using LWMA-3 algorithm
func CalcDifficultyLWMA(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	if parent.Number.Uint64() < LWMAWindowSize {
		return big.NewInt(LWMAMinDifficulty)
	}

	var (
		blockTimes            = make([]uint64, LWMAWindowSize)
		difficulties          = make([]*big.Int, LWMAWindowSize)
		weightedSolveTimeSum  = big.NewInt(0)
		weightSum             = big.NewInt(0)
		weightedDifficultySum = big.NewInt(0)
		currentBlock          = parent
	)

	// Collect last N blocks
	for i := LWMAWindowSize - 1; i >= 0; i-- {
		if currentBlock == nil || currentBlock.Number.Uint64() == 0 {
			return big.NewInt(LWMAMinDifficulty)
		}
		blockTimes[i] = currentBlock.Time
		difficulties[i] = new(big.Int).Set(currentBlock.Difficulty)
		if i > 0 {
			currentBlock = chain.GetHeader(currentBlock.ParentHash, currentBlock.Number.Uint64()-1)
		}
	}

	// Detect hashrate burst attack by analyzing recent solve time variance
	burstDetected := detectHashrateBurst(blockTimes, LWMAWindowSize)

	// Calculate LWMA with burst protection
	for i := 0; i < LWMAWindowSize-1; i++ {
		solveTime := blockTimes[i+1] - blockTimes[i]
		if solveTime == 0 {
			solveTime = 1
		}
		// Extended clipping for anti-burst protection
		// Cap extremely slow blocks at 10× target time (was 6×)
		if solveTime > 10*LWMATargetBlockTime {
			solveTime = 10 * LWMATargetBlockTime
		}

		weight := int64(i + 1)
		bigWeight := big.NewInt(weight)
		bigSolveTime := big.NewInt(int64(solveTime))

		temp1 := new(big.Int).Mul(bigSolveTime, bigWeight)
		weightedSolveTimeSum.Add(weightedSolveTimeSum, temp1)
		weightSum.Add(weightSum, bigWeight)
		temp2 := new(big.Int).Mul(temp1, difficulties[i])
		weightedDifficultySum.Add(weightedDifficultySum, temp2)
	}

	nextDifficulty := new(big.Int).Div(weightedDifficultySum, weightedSolveTimeSum)

	// Apply damping if burst detected to prevent difficulty crash after attacker leaves
	if burstDetected {
		// Damping: blend 90% new difficulty + 10% parent difficulty
		dampingNum := big.NewInt(9)   // 90%
		dampingDen := big.NewInt(10)  // 100%

		dampedDiff := new(big.Int).Mul(nextDifficulty, dampingNum)
		dampedDiff.Div(dampedDiff, dampingDen)

		parentContrib := new(big.Int).Sub(dampingDen, dampingNum)
		parentPart := new(big.Int).Mul(parent.Difficulty, parentContrib)
		parentPart.Div(parentPart, dampingDen)

		nextDifficulty = new(big.Int).Add(dampedDiff, parentPart)
	}

	minDiff := big.NewInt(LWMAMinDifficulty)
	if nextDifficulty.Cmp(minDiff) < 0 {
		nextDifficulty.Set(minDiff)
	}

	maxIncrease := new(big.Int).Mul(parent.Difficulty, big.NewInt(LWMAMaxAdjustmentUp))
	if nextDifficulty.Cmp(maxIncrease) > 0 {
		nextDifficulty.Set(maxIncrease)
	}

	maxDecrease := new(big.Int).Div(parent.Difficulty, big.NewInt(LWMAMaxAdjustmentDown))
	if nextDifficulty.Cmp(maxDecrease) < 0 {
		nextDifficulty.Set(maxDecrease)
	}

	return nextDifficulty
}

// ShouldUseLWMA determines whether to use LWMA
func ShouldUseLWMA(config *params.ChainConfig, blockNumber *big.Int) bool {
	if config.RandomX != nil && config.RandomX.LWMAActivationBlock != nil {
		return blockNumber.Cmp(config.RandomX.LWMAActivationBlock) >= 0
	}
	if config.RandomX != nil {
		return true
	}
	return false
}

// detectHashrateBurst detects sudden hashrate changes that indicate burst mining attack
// Returns true if suspicious burst pattern detected in recent blocks
func detectHashrateBurst(blockTimes []uint64, windowSize int) bool {
	if windowSize < LWMABurstDetectionWindow+1 {
		return false // Not enough blocks
	}

	// Analyze last LWMABurstDetectionWindow blocks
	recentStart := windowSize - LWMABurstDetectionWindow - 1
	recentSolveTimes := make([]uint64, 0, LWMABurstDetectionWindow)

	// Calculate solve times for recent window
	for i := recentStart; i < windowSize-1; i++ {
		solveTime := blockTimes[i+1] - blockTimes[i]
		if solveTime == 0 {
			solveTime = 1
		}
		recentSolveTimes = append(recentSolveTimes, solveTime)
	}

	// Calculate average and variance of recent solve times
	var sum uint64
	for _, t := range recentSolveTimes {
		sum += t
	}
	avgSolveTime := sum / uint64(len(recentSolveTimes))

	// Calculate variance
	var varianceSum uint64
	for _, t := range recentSolveTimes {
		diff := int64(t) - int64(avgSolveTime)
		if diff < 0 {
			diff = -diff
		}
		varianceSum += uint64(diff * diff)
	}
	variance := varianceSum / uint64(len(recentSolveTimes))
	stdDev := uint64(0)

	// Simple integer square root for standard deviation
	if variance > 0 {
		x := variance
		for {
			root := (x + variance/x) / 2
			if root >= x {
				break
			}
			x = root
		}
		stdDev = x
	}

	// Burst detected if:
	// 1. Recent average solve time is much faster than target (< 50% target)
	// 2. High variance (stdDev > 2× average) indicating unstable hashrate
	fastBurst := avgSolveTime < (LWMATargetBlockTime / 2)
	highVariance := stdDev > (2 * avgSolveTime)

	// Additional check: detect if most recent blocks are consistently fast
	veryFastBlocks := 0
	for _, t := range recentSolveTimes {
		if t < (LWMATargetBlockTime / 2) {
			veryFastBlocks++
		}
	}
	sustainedBurst := veryFastBlocks > (LWMABurstDetectionWindow * 6 / 10) // 60%+ fast blocks

	return (fastBurst && highVariance) || sustainedBurst
}
