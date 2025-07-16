// SPDX-License-Identifier: BUSL 1.1
pragma solidity 0.8.26;

import {WorkflowRegistry} from "../../WorkflowRegistry.sol";
import {LinkingUtils} from "../../testhelpers/LinkingUtils.sol";
import {WorkflowRegistrySetup} from "./WorkflowRegistrySetup.t.sol";

contract WorkflowRegistry_canUnlinkOwner is WorkflowRegistrySetup {
  modifier whenOwnerIsLinked() {
    _linkOwner(s_owner);
    _;
  }

  // whenPreUnlinkActionIsNONE
  function test_canUnlinkOwner_WhenOwnerHasActiveWorkflows() external whenOwnerIsLinked {
    // it should revert with CannotUnlinkWithActiveWorkflows
    _setDONLimit();
    _upsertTestWorklows(WorkflowRegistry.WorkflowStatus.ACTIVE, false, s_owner);

    (, bytes memory sig) = _getUnlinkProofSignature(s_owner);
    vm.expectRevert(abi.encodeWithSelector(WorkflowRegistry.CannotUnlinkWithActiveWorkflows.selector));
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.NONE);
  }

  // whenPreUnlinkActionIsNONE
  // whenOwnerHasNoActiveWorkflows
  function test_canUnlinkOwner_WhenValidTimestampIsLessThanBlockTimestamp() external whenOwnerIsLinked {
    // it should revert with UnlinkOwnerRequestExpired
    // block time has advanced by 24 hours so the validity timestamp is in the past
    vm.warp(block.timestamp + 24 hours);
    (, bytes memory sig) = _getUnlinkProofSignature(s_owner);
    vm.expectRevert(
      abi.encodeWithSelector(
        WorkflowRegistry.UnlinkOwnerRequestExpired.selector, s_owner, block.timestamp, s_validityTimestamp
      )
    );
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.NONE);
    assertTrue(s_registry.isOwnerLinked(s_owner), "Owner should be linked");
  }

  // whenPreUnlinkActionIsNONE
  // whenOwnerHasNoActiveWorkflows
  // whenValidTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenOwnerIsNotYetLinked() external {
    // it should revert with OwnershipLinkDoesNotExist
    (, bytes memory sig) = _getUnlinkProofSignature(s_user);
    vm.expectRevert(abi.encodeWithSelector(WorkflowRegistry.OwnershipLinkDoesNotExist.selector, s_owner));
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.NONE);
    assertFalse(s_registry.isOwnerLinked(s_owner), "Owner should not be linked");
  }

  // whenPreUnlinkActionIsNONE
  // whenOwnerHasNoActiveWorkflows
  // whenValidTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenTheSignatureDoesNotRecoverAnAllowedSigner() external whenOwnerIsLinked {
    // it should revert with InvalidOwnershipLink
    (bytes32 ownerProof,) = _getUnlinkProofSignature(s_owner);
    uint256 randomPrivateKey = 0x7f3c2a9b5d4e1f8c0b2d3a4e5f6c7d8e9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d;
    (uint8 v, bytes32 r, bytes32 s) = vm.sign(
      randomPrivateKey,
      LinkingUtils.getMessageHash(
        LinkingUtils.REQUEST_TYPE_LINK, address(s_registry), s_owner, s_validityTimestamp, ownerProof
      )
    );

    bytes memory sig = abi.encodePacked(r, s, v);
    vm.expectRevert(
      abi.encodeWithSelector(
        WorkflowRegistry.InvalidOwnershipLink.selector, s_owner, s_validityTimestamp, ownerProof, sig
      )
    );
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.NONE);
  }

  // whenPreUnlinkActionIsNONE
  // whenOwnerHasNoActiveWorkflows
  // whenValidTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenTheSignatureRecoversAnAllowedSigner() external whenOwnerIsLinked {
    // it should return (no revert)
    (, bytes memory sig) = _getUnlinkProofSignature(s_owner);
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.NONE);
  }

  // whenPreUnlinkeActionIsPAUSE_WORKFLOWS_Or_REMOVE_WORKFLOWS
  function test_canUnlinkOwner_WhenValidityTimestampIsLessThanBlockTimestamp() external {
    // it should revert with UnlinkOwnerRequestExpired
    // block time has advanced by 24 hours so the validity timestamp is in the past
    vm.warp(block.timestamp + 24 hours);
    (, bytes memory sig) = _getUnlinkProofSignature(s_owner);
    vm.expectRevert(
      abi.encodeWithSelector(
        WorkflowRegistry.UnlinkOwnerRequestExpired.selector, s_owner, block.timestamp, s_validityTimestamp
      )
    );
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.PAUSE_WORKFLOWS);
  }

  // whenPreUnlinkeActionIsPAUSE_WORKFLOWSOrREMOVE_WORKFLOWS
  // whenValidityTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenOwnerIsNotLinked() external {
    // it should revert with OwnershipLinkDoesNotExist(owner)
    (, bytes memory sig) = _getUnlinkProofSignature(s_user);
    vm.expectRevert(abi.encodeWithSelector(WorkflowRegistry.OwnershipLinkDoesNotExist.selector, s_owner));
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.PAUSE_WORKFLOWS);
  }

  //   whenPreUnlinkeActionIsPAUSE_WORKFLOWSOrREMOVE_WORKFLOWS
  //   whenValidityTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenSignatureDoesNotRecoverAnAllowedSigner() external whenOwnerIsLinked {
    // it should revert with InvalidOwnershipLink
    (bytes32 ownerProof,) = _getUnlinkProofSignature(s_owner);
    uint256 randomPrivateKey = 0x7f3c2a9b5d4e1f8c0b2d3a4e5f6c7d8e9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d;
    (uint8 v, bytes32 r, bytes32 s) = vm.sign(
      randomPrivateKey,
      LinkingUtils.getMessageHash(
        LinkingUtils.REQUEST_TYPE_LINK, address(s_registry), s_owner, s_validityTimestamp, ownerProof
      )
    );

    bytes memory sig = abi.encodePacked(r, s, v);
    vm.expectRevert(
      abi.encodeWithSelector(
        WorkflowRegistry.InvalidOwnershipLink.selector, s_owner, s_validityTimestamp, ownerProof, sig
      )
    );
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.PAUSE_WORKFLOWS);
  }

  // whenPreUnlinkeActionIsPAUSE_WORKFLOWSOrREMOVE_WORKFLOWS
  // whenValidityTimestampGreaterThanOrEqualToBlockTimestamp
  function test_canUnlinkOwner_WhenSignatureRecoversAnAllowedSigner() external whenOwnerIsLinked {
    // it should return with no errors
    (, bytes memory sig) = _getUnlinkProofSignature(s_owner);
    s_registry.canUnlinkOwner(s_owner, s_validityTimestamp, sig, WorkflowRegistry.PreUnlinkAction.REMOVE_WORKFLOWS);
  }
}
