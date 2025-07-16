// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;

import {Script,console} from "forge-std/Script.sol";
import {FundMe} from "../src/FundMe.sol";
import {Help} from "./help.s.sol";

contract FundmeDeploy is Script {
    function run() external returns (FundMe) {

        Help help = new Help();
        address priceFeedAddress = help.activenetwork();
        
        vm.startBroadcast();
        FundMe fundMe = new FundMe(priceFeedAddress);
        vm.stopBroadcast();
        return fundMe;

    }
}