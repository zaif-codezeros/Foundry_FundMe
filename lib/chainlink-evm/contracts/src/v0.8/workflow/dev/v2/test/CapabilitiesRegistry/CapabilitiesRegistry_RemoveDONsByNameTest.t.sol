// SPDX-License-Identifier: MIT
pragma solidity ^0.8.26;

import {Ownable2Step} from "../../../../../shared/access/Ownable2Step.sol";
import {CapabilitiesRegistry} from "../../CapabilitiesRegistry.sol";
import {BaseTest} from "./BaseTest.t.sol";

contract CapabilitiesRegistry_RemoveDONsByNameTest is BaseTest {
  function setUp() public override {
    BaseTest.setUp();

    CapabilitiesRegistry.Capability[] memory capabilities = new CapabilitiesRegistry.Capability[](2);
    capabilities[0] = s_basicCapability;
    capabilities[1] = s_capabilityWithConfigurationContract;

    s_CapabilitiesRegistry.addNodeOperators(_getNodeOperators());
    s_CapabilitiesRegistry.addCapabilities(s_capabilities);

    vm.stopPrank();
    vm.startPrank(NODE_OPERATOR_ONE_ADMIN);
    s_CapabilitiesRegistry.addNodes(s_paramsForTwoNodes);

    CapabilitiesRegistry.CapabilityConfiguration[] memory capabilityConfigs =
      new CapabilitiesRegistry.CapabilityConfiguration[](1);
    capabilityConfigs[0] =
      CapabilitiesRegistry.CapabilityConfiguration({capabilityId: s_basicCapabilityId, config: BASIC_CAPABILITY_CONFIG});

    bytes32[] memory nodeIds = new bytes32[](2);
    nodeIds[0] = P2P_ID;
    nodeIds[1] = P2P_ID_TWO;

    vm.stopPrank();
    vm.startPrank(ADMIN);
    CapabilitiesRegistry.NewDONParams[] memory newDONs = new CapabilitiesRegistry.NewDONParams[](1);
    newDONs[0] = CapabilitiesRegistry.NewDONParams({
      nodes: nodeIds,
      capabilityConfigurations: capabilityConfigs,
      isPublic: true,
      acceptsWorkflows: true,
      f: 1,
      name: TEST_DON_NAME_ONE,
      donFamilies: new string[](0),
      config: TEST_DON_CONFIG
    });
    s_CapabilitiesRegistry.addDONs(newDONs);
  }

  function test_RevertWhen_CalledByNonAdmin() public {
    string[] memory donNames = new string[](1);
    donNames[0] = TEST_DON_NAME_ONE;
    changePrank(STRANGER);
    vm.expectRevert(abi.encodeWithSelector(Ownable2Step.OnlyCallableByOwner.selector));
    s_CapabilitiesRegistry.removeDONsByName(donNames);
  }

  function test_RemovesDONsByName() public {
    vm.expectEmit(true, true, true, true, address(s_CapabilitiesRegistry));
    emit CapabilitiesRegistry.ConfigSet(DON_ID, 0);

    string[] memory donNames = new string[](1);
    donNames[0] = TEST_DON_NAME_ONE;
    s_CapabilitiesRegistry.removeDONsByName(donNames);

    vm.expectRevert(abi.encodeWithSelector(CapabilitiesRegistry.DONDoesNotExist.selector, DON_ID));
    CapabilitiesRegistry.DONInfo memory donInfo = s_CapabilitiesRegistry.getDON(DON_ID);

    (bytes memory CapabilitiesRegistryDONConfig, bytes memory capabilityConfigContractConfig) =
      s_CapabilitiesRegistry.getCapabilityConfigs(DON_ID, s_basicCapabilityId);

    assertEq(CapabilitiesRegistryDONConfig, bytes(""));
    assertEq(capabilityConfigContractConfig, bytes(""));
    assertEq(donInfo.nodeP2PIds.length, 0);

    assertEq(s_CapabilitiesRegistry.isDONNameTaken(TEST_DON_NAME_ONE), false);
  }
}
