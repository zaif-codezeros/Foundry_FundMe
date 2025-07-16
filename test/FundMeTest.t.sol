// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;

import {Test, console} from "forge-std/Test.sol";
import {FundMe} from "../src/FundMe.sol";
import {FundmeDeploy} from "../script/Deploy.s.sol";

contract FundMeTest is Test{
    FundMe fundMe;
    
    function setUp() external {
        //fundMe = new FundMe(0x694AA1769357215DE4FAC081bf1f309aDC325306);
        FundmeDeploy deployFundme = new FundmeDeploy();
        fundMe = deployFundme.run();
    }

    function testMinUsd() public view {
        assertEq(fundMe.MINIMUM_USD(),5e18);
        console.log("all set");
    }
    
    function testOwner() public view {
        assertEq(fundMe.i_owner(), msg.sender);
        console.log("all set");
    }

    function testversion() public view {
        uint256 version = fundMe.getVersion();
        assertEq(version,4);
        console.log("Chainlink price feed version:", version);
    }
}