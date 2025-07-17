// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;

import {Script,console} from "forge-std/Script.sol";
import {DevOpsTools} from "../lib/foundry-devops/src/DevOpsTools.sol";
import {FundMe} from "../src/FundMe.sol";

contract FundFundMe is Script {

    uint256 constant SEND_VALUE = 1e18; // 1 ETH in wei

    function fundFundMe(address mostRecentDeploy) public {
        vm.startBroadcast();
        FundMe(payable(mostRecentDeploy)).fund{value: SEND_VALUE}();
        vm.stopBroadcast();
        console.log("Funded FundMe contract at address:", mostRecentDeploy);
    }


    function run() external {
        address mostRecentDeploy = DevOpsTools.get_most_recent_deployment(
            "FundMe",
            block.chainid
        );
        fundFundMe(mostRecentDeploy);
    }


}

contract withdrawFundMe is Script {

    uint256 constant SEND_VALUE = 1e18; // 1 ETH in wei

    function withdrawFundme(address mostRecentDeploy) public {
        vm.startBroadcast();
        FundMe(payable(mostRecentDeploy)).withdraw();
        vm.stopBroadcast();
    }


    function run() external {
        address mostRecentDeploy = DevOpsTools
        .get_most_recent_deployment(
            "FundMe",
            block.chainid
        );
        withdrawFundme(mostRecentDeploy);
    }

}
