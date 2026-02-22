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


package core

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// DefaultDucrosGenesisBlock returns the Ducros mainnet genesis block.
func DefaultDucrosGenesisBlock() *Genesis {
	return &Genesis{
		Config: &params.ChainConfig{
			ChainID:             big.NewInt(33669),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			ShanghaiTime:        nil,  // Disabled - RandomX PoW doesn't support Shanghai (withdrawals)
			CancunTime:          nil,  // Disabled - RandomX PoW doesn't support Cancun (blobs)
			PragueTime:          nil,
			OsakaTime:           nil,
			RandomX: &params.RandomXConfig{
				LWMAActivationBlock: big.NewInt(0),
			},
		},
		Nonce:      0x42,
		Timestamp:  0x0,
		ExtraData:  hexutil.MustDecode("0x447563726f73207c204d61696e6e6574"),
		GasLimit:   0xC65D40,
		Difficulty: big.NewInt(1),
		Mixhash:    common.Hash{},
		Coinbase:   common.Address{},
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Alloc:      types.GenesisAlloc{
    		common.HexToAddress("0xC1dA1A32d74d268eF4aAA12561dcD9a8b374146E"): {Balance: mustBig("30000000000000000000000000")},
    		common.HexToAddress("0x5f2f91b9d446eA92784fe4B2adF32236219de6c3"): {Balance: mustBig("15000000000000000000000000")},
    		common.HexToAddress("0xe249b5c46b43e6796c2F26999A2C00D47Dc49D1f"): {Balance: mustBig("10000000000000000000000000")},
    		common.HexToAddress("0xb8B4180fe9564528432ff93a88aC15a61FdcC4Da"): {Balance: mustBig("15000000000000000000000000")},
    		common.HexToAddress("0x87B33f4Ba0919bf0E98358468BA7FA33Dc349Ac2"): {Balance: mustBig("5000000000000000000000000")},
		},
	}
}

// DefaultDucrosTestnetGenesisBlock returns the Ducros testnet genesis block.
func DefaultDucrosTestnetGenesisBlock() *Genesis {
	return &Genesis{
		Config: &params.ChainConfig{
			ChainID:             big.NewInt(336690),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			ShanghaiTime:        nil,  // Disabled - RandomX PoW doesn't support Shanghai (withdrawals)
			CancunTime:          nil,  // Disabled - RandomX PoW doesn't support Cancun (blobs)
			PragueTime:          nil,
			OsakaTime:           nil,
			RandomX: &params.RandomXConfig{
				LWMAActivationBlock: big.NewInt(0),
			},
		},
		Nonce:      0x42,
		Timestamp:  0x0,
		ExtraData:  hexutil.MustDecode("0x447563726f73207c20546573746e6574"),
		GasLimit:   0xC65D40,
		Difficulty: big.NewInt(1),
		Mixhash:    common.Hash{},
		Coinbase:   common.Address{},
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Alloc:      types.GenesisAlloc{},
	}
}

func newUint64(val uint64) *uint64 { return &val }

func mustBig(s string) *big.Int {
    n, ok := new(big.Int).SetString(s, 10)
    if !ok {
        panic("invalid big int: " + s)
    }
    return n
}