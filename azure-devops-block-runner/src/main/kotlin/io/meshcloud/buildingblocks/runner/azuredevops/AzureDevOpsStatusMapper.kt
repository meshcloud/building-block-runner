package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRunResult
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRunState
import io.meshcloud.buildingblocks.runner.azuredevops.client.TimelineRecordResult
import io.meshcloud.buildingblocks.runner.azuredevops.client.TimelineRecordState
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

object AzureDevOpsStatusMapper {
  fun mapPipelineResultToStatus(result: PipelineRunResult?): MeshBuildingBlockRun.ExecutionStatus = when (result) {
    PipelineRunResult.SUCCEEDED -> MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED
    else -> MeshBuildingBlockRun.ExecutionStatus.FAILED
  }

  fun mapPipelineResultToUserMessage(result: PipelineRunResult?): String = when (result) {
    PipelineRunResult.SUCCEEDED -> "Azure DevOps pipeline completed successfully"
    PipelineRunResult.FAILED -> "Azure DevOps pipeline failed"
    PipelineRunResult.CANCELED -> "Azure DevOps pipeline was canceled"
    else -> "Azure DevOps pipeline completed with unknown status"
  }

  fun mapPipelineStateToUserMessage(state: PipelineRunState?): String = when (state) {
    PipelineRunState.IN_PROGRESS -> "Azure DevOps pipeline is running"
    PipelineRunState.COMPLETED -> "Azure DevOps pipeline has completed"
    null -> "Azure DevOps pipeline state is unknown"
    else -> "Azure DevOps pipeline state: ${state.value}"
  }

  fun mapStageToStatus(state: TimelineRecordState?, result: TimelineRecordResult?): MeshBuildingBlockRun.ExecutionStatus = when (state) {
    TimelineRecordState.PENDING,
    TimelineRecordState.IN_PROGRESS -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS

    TimelineRecordState.COMPLETED -> when (result) {
      TimelineRecordResult.SUCCEEDED,
      TimelineRecordResult.SKIPPED -> MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED

      TimelineRecordResult.FAILED,
      TimelineRecordResult.CANCELED,
      TimelineRecordResult.ABANDONED -> MeshBuildingBlockRun.ExecutionStatus.FAILED

      else -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS
    }

    else -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS
  }

  fun mapStageToUserMessage(stageName: String, state: TimelineRecordState?, result: TimelineRecordResult?): String = when {
    state == TimelineRecordState.PENDING -> "$stageName is pending"
    state == TimelineRecordState.IN_PROGRESS -> "$stageName is running"
    state == TimelineRecordState.COMPLETED && result == TimelineRecordResult.SUCCEEDED -> "$stageName completed successfully"
    state == TimelineRecordState.COMPLETED && result == TimelineRecordResult.SKIPPED -> "$stageName was skipped"
    state == TimelineRecordState.COMPLETED && result == TimelineRecordResult.FAILED -> "$stageName failed"
    state == null -> "$stageName is in unknown state"
    else -> "$stageName: ${state.value}"
  }

  fun buildStageSystemMessage(stageName: String, state: TimelineRecordState?, result: TimelineRecordResult?, startTime: String?, finishTime: String?): String = buildString {
    val stateString = state?.value ?: "Unknown"
    append("Stage: $stageName, State: $stateString")
    if (result != null) append(", Result: ${result.value}")
    if (startTime != null) append(", Started: $startTime")
    if (finishTime != null) append(", Finished: $finishTime")
  }
}

