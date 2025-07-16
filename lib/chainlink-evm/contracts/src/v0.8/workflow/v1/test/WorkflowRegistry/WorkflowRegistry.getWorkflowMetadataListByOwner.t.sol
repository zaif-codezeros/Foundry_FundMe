// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {WorkflowRegistry} from "../../WorkflowRegistry.sol";
import {WorkflowRegistryWithFixture} from "./WorkflowRegistryWithFixture.t.sol";

contract WorkflowRegistry_getWorkflowMetadataListByOwner is WorkflowRegistryWithFixture {
  function test_WhenStartIs0() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 0, 0);

    assertEq(workflows.length, 3);
    assertEq(workflows[0].workflowName, s_workflowName1);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].workflowID, s_workflowID1);
    assertEq(workflows[0].binaryURL, s_binaryURL1);
    assertEq(workflows[0].configURL, s_configURL1);
    assertEq(workflows[0].secretsURL, s_secretsURL1);

    assertEq(workflows[1].workflowName, s_workflowName2);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].workflowID, s_workflowID2);
    assertEq(workflows[1].binaryURL, s_binaryURL2);
    assertEq(workflows[1].configURL, s_configURL2);
    assertEq(workflows[1].secretsURL, s_secretsURL2);

    assertEq(workflows[2].workflowName, s_workflowName3);
    assertEq(workflows[2].owner, s_authorizedAddress);
    assertEq(workflows[2].donID, s_allowedDonID);
    assertTrue(workflows[2].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[2].workflowID, s_workflowID3);
    assertEq(workflows[2].binaryURL, s_binaryURL3);
    assertEq(workflows[2].configURL, s_configURL3);
    assertEq(workflows[2].secretsURL, s_secretsURL3);
  }

  function test_WhenStartIsGreaterThan0() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 1, 3);

    assertEq(workflows.length, 2);
    assertEq(workflows[0].workflowName, s_workflowName2);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].workflowID, s_workflowID2);
    assertEq(workflows[0].binaryURL, s_binaryURL2);
    assertEq(workflows[0].configURL, s_configURL2);
    assertEq(workflows[0].secretsURL, s_secretsURL2);

    assertEq(workflows[1].workflowName, s_workflowName3);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].workflowID, s_workflowID3);
    assertEq(workflows[1].binaryURL, s_binaryURL3);
    assertEq(workflows[1].configURL, s_configURL3);
    assertEq(workflows[1].secretsURL, s_secretsURL3);
  }

  function test_WhenLimitIsLessThanTotalWorkflows() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 0, 2);

    assertEq(workflows.length, 2);
    assertEq(workflows[0].workflowName, s_workflowName1);
    assertEq(workflows[0].workflowID, s_workflowID1);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].binaryURL, s_binaryURL1);
    assertEq(workflows[0].configURL, s_configURL1);
    assertEq(workflows[0].secretsURL, s_secretsURL1);

    assertEq(workflows[1].workflowName, s_workflowName2);
    assertEq(workflows[1].workflowID, s_workflowID2);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].binaryURL, s_binaryURL2);
    assertEq(workflows[1].configURL, s_configURL2);
    assertEq(workflows[1].secretsURL, s_secretsURL2);
  }

  function test_WhenLimitIsEqualToTotalWorkflows() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 0, 3);

    assertEq(workflows.length, 3);
    assertEq(workflows[0].workflowName, s_workflowName1);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].workflowID, s_workflowID1);
    assertEq(workflows[0].binaryURL, s_binaryURL1);
    assertEq(workflows[0].configURL, s_configURL1);
    assertEq(workflows[0].secretsURL, s_secretsURL1);

    assertEq(workflows[1].workflowName, s_workflowName2);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].workflowID, s_workflowID2);
    assertEq(workflows[1].binaryURL, s_binaryURL2);
    assertEq(workflows[1].configURL, s_configURL2);
    assertEq(workflows[1].secretsURL, s_secretsURL2);

    assertEq(workflows[2].workflowName, s_workflowName3);
    assertEq(workflows[2].owner, s_authorizedAddress);
    assertEq(workflows[2].donID, s_allowedDonID);
    assertTrue(workflows[2].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[2].workflowID, s_workflowID3);
    assertEq(workflows[2].binaryURL, s_binaryURL3);
    assertEq(workflows[2].configURL, s_configURL3);
    assertEq(workflows[2].secretsURL, s_secretsURL3);
  }

  function test_WhenLimitExceedsTotalWorkflows() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 0, 10);

    assertEq(workflows.length, 3);
    assertEq(workflows[0].workflowName, s_workflowName1);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].workflowID, s_workflowID1);
    assertEq(workflows[0].binaryURL, s_binaryURL1);
    assertEq(workflows[0].configURL, s_configURL1);
    assertEq(workflows[0].secretsURL, s_secretsURL1);

    assertEq(workflows[1].workflowName, s_workflowName2);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].workflowID, s_workflowID2);
    assertEq(workflows[1].binaryURL, s_binaryURL2);
    assertEq(workflows[1].configURL, s_configURL2);
    assertEq(workflows[1].secretsURL, s_secretsURL2);

    assertEq(workflows[2].workflowName, s_workflowName3);
    assertEq(workflows[2].owner, s_authorizedAddress);
    assertEq(workflows[2].donID, s_allowedDonID);
    assertTrue(workflows[2].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[2].workflowID, s_workflowID3);
    assertEq(workflows[2].binaryURL, s_binaryURL3);
    assertEq(workflows[2].configURL, s_configURL3);
    assertEq(workflows[2].secretsURL, s_secretsURL3);
  }

  function test_WhenTheOwnerHasNoWorkflows() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_unauthorizedAddress, 0, 10);
    assertEq(workflows.length, 0);
  }

  function test_WhenStartIsGreaterThanOrEqualToTotalWorkflows() external view {
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 10, 1);

    assertEq(workflows.length, 0);
  }

  function test_WhenTheRegistryIsLocked() external {
    // Lock the registry
    vm.prank(s_owner);
    s_registry.lockRegistry();

    // It should behave the same as when the registry is not locked
    vm.prank(s_stranger);
    WorkflowRegistry.WorkflowMetadata[] memory workflows =
      s_registry.getWorkflowMetadataListByOwner(s_authorizedAddress, 0, 0);

    assertEq(workflows.length, 3);
    assertEq(workflows[0].workflowName, s_workflowName1);
    assertEq(workflows[0].owner, s_authorizedAddress);
    assertEq(workflows[0].donID, s_allowedDonID);
    assertTrue(workflows[0].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[0].workflowID, s_workflowID1);
    assertEq(workflows[0].binaryURL, s_binaryURL1);
    assertEq(workflows[0].configURL, s_configURL1);
    assertEq(workflows[0].secretsURL, s_secretsURL1);

    assertEq(workflows[1].workflowName, s_workflowName2);
    assertEq(workflows[1].owner, s_authorizedAddress);
    assertEq(workflows[1].donID, s_allowedDonID);
    assertTrue(workflows[1].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[1].workflowID, s_workflowID2);
    assertEq(workflows[1].binaryURL, s_binaryURL2);
    assertEq(workflows[1].configURL, s_configURL2);
    assertEq(workflows[1].secretsURL, s_secretsURL2);

    assertEq(workflows[2].workflowName, s_workflowName3);
    assertEq(workflows[2].owner, s_authorizedAddress);
    assertEq(workflows[2].donID, s_allowedDonID);
    assertTrue(workflows[2].status == WorkflowRegistry.WorkflowStatus.ACTIVE);
    assertEq(workflows[2].workflowID, s_workflowID3);
    assertEq(workflows[2].binaryURL, s_binaryURL3);
    assertEq(workflows[2].configURL, s_configURL3);
    assertEq(workflows[2].secretsURL, s_secretsURL3);
  }
}
