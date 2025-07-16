// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;

import {Script} from "forge-std/Script.sol";

contract Help is Script {
    
    NetworkConfig public activenetwork;

    struct NetworkConfig {
        address priceFeed;
    }

    constructor(){
        if (block.chainid == 11155111) {
            activenetwork = getSepoliaConfig();
        } else if (block.chainid == 1) {
            activenetwork = getEtheriumConfig();
        } else {
            activenetwork = getAnvilconfig();
        }
    }

    function getSepoliaConfig() public pure returns (NetworkConfig memory) {
        NetworkConfig memory seploiaconfig = NetworkConfig({
            priceFeed: 0x694AA1769357215DE4FAC081bf1f309aDC325306 // Sepolia ETH/USD Price Feed
        });
        return seploiaconfig;
    }

    function getEtheriumConfig() public pure returns (NetworkConfig memory) {
        NetworkConfig memory etheriumconfig = NetworkConfig({
            priceFeed: 0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419 // Ethereum Mainnet ETH/USD Price Feed
        });
        return etheriumconfig;
    }

    function getAnvilconfig() public pure returns (NetworkConfig memory) {
        NetworkConfig memory anvilconfig = NetworkConfig({
            priceFeed: 0x694AA1769357215DE4FAC081bf1f309aDC325306 // Anvil ETH/USD Price Feed
        });
        return anvilconfig;
    }
}