// SPDX-License-Identifier: SEE LICENSE IN LICENSE
pragma solidity 0.8.30;


import {Test,console} from "forge-std/Test.sol";
import {FundMe} from "../../src/FundMe.sol";
import {FundmeDeploy} from "../../script/Deploy.s.sol";
import {FundFundMe, withdrawFundMe} from "../../script/interactions.s.sol";


contract interactionTest is Test {
    FundMe fundme;

    address user = makeAddr("user");
    uint256 constant SEND_VALUE = 1 ether;
    uint256 constant STARTING_BALANCE = 10 ether;
    uint256 constant GAS_PRICE = 1;

    function setUp() external {
        
        FundmeDeploy deployFundme = new FundmeDeploy();
        (fundme, ) = deployFundme.run();
        vm.deal(user, STARTING_BALANCE);
    }

    function testUsercanFund() public {
        FundFundMe fundFundMe = new FundFundMe();
        fundFundMe.fundFundMe(address(fundme));

        withdrawFundMe withdrawInstance = new withdrawFundMe();
        withdrawInstance.withdrawFundme(address(fundme));

        assert(address(fundme).balance == 0);
    }

}