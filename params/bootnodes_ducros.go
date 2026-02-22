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

package params

// DucrosBootnodes are the enode URLs of the P2P bootstrap nodes running on
// the Ducros network.
var DucrosBootnodes = []string{
	// Ducros Mainnet Bootstrap Nodes - Infrastructure
	//
	// Bootnode #1 - VPS Primary
	"enode://54b42119d1fcbf52a79668dd84fbd60874ba3c67bb77c90a8492432044db379049ff33892d0f2800cae93711278421b8e49d37b3f51b1022ef79cc65361f9b34@51.178.46.223:30310",

	// TODO: Add more bootnodes for redundancy
	// Format: "enode://[public_key]@[IP_or_domain]:[port]"
}

// DucrosTestnetBootnodes are the enode URLs of the P2P bootstrap nodes running on
// the Ducros test network.
var DucrosTestnetBootnodes = []string{
	// Ducros Testnet Bootnodes
	// TODO: Add testnet bootnodes for development
	// Example format:
	// "enode://[public_key]@[IP_or_domain]:[port]",
}
