// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;

import {Script} from "forge-std/Script.sol";
import {MockV3Aggregator} from "../test/mock/MockV3Aggregator.sol";

contract Help is Script {
    
    NetworkConfig public activenetwork;

    struct NetworkConfig {
        address priceFeed;
        uint256 version;
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
            priceFeed: 0x694AA1769357215DE4FAC081bf1f309aDC325306 ,version : 4// Sepolia ETH/USD Price Feed
        });
        return seploiaconfig;
    }

    function getEtheriumConfig() public pure returns (NetworkConfig memory) {
        NetworkConfig memory etheriumconfig = NetworkConfig({
            priceFeed: 0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419 , version : 6// Ethereum Mainnet ETH/USD Price Feed
        });
        return etheriumconfig;
    }

    function getAnvilconfig() public  returns (NetworkConfig memory) {
        
        if(activenetwork.priceFeed != address(0)) {
            return activenetwork; // Return existing config if already set
        }
        
        vm.startBroadcast();
        MockV3Aggregator mockPricefeed = new MockV3Aggregator(8, 2000e8); // 2000 USD in 8 decimals
        vm.stopBroadcast();

        NetworkConfig memory anvilconfig = NetworkConfig({
            priceFeed: address(mockPricefeed), version : 4// Mock Price Feed for Anvil
        });
        return anvilconfig;
    }
}