package io.meshcloud.buildingblocks.runner.azuredevops

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRun
import io.meshcloud.buildingblocks.runner.azuredevops.client.TimelineRecord
import io.meshcloud.buildingblocks.runner.azuredevops.client.TimelineRecordState
import io.meshcloud.buildingblocks.runner.azuredevops.client.TimelineRecordType
import io.meshcloud.buildingblocks.runner.http.MeshHttpException
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

private val log = KotlinLogging.logger { }

class AzureDevOpsStatusUpdater(
  private val blockRunClient: BlockRunClient,
  private val blockRunUuid: String,
) {

  fun updateFinalBlockStatusFromPipeline(
    pipelineRun: PipelineRun,
  ) {
    val status = AzureDevOpsStatusMapper.mapPipelineResultToStatus(pipelineRun.result)
    val userMessage = AzureDevOpsStatusMapper.mapPipelineResultToUserMessage(pipelineRun.result)
    val webUrl = pipelineRun.links?.get("web")?.get("href") ?: pipelineRun.url ?: "N/A"
    val systemMessage =
      "Pipeline run ${pipelineRun.id} completed with state: ${pipelineRun.state}, result: ${pipelineRun.result}. View run: $webUrl"
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = status,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = AzureDevOpsBlockRunnerService.STEP_ID,
          status = status,
          userMessage = userMessage,
          systemMessage = systemMessage,
        ),
      ),
    )
    log.info { "Azure DevOps pipeline ${pipelineRun.id} completed with result '${pipelineRun.result}' for run: $blockRunUuid" }
    blockRunClient.updateBlockRun(update)
  }

  fun updateFailedBlockStatusWithMeshException(ex: MeshHttpException) {
    log.error(ex) { "Error contacting Azure DevOps" }
    updateFailedBlockStatusWithMessage(
      "Request: ${ex.requestUrl}\nAzure DevOps responded with status: ${ex.statusCode} and body: ${ex.getResponseBody()}",
    )
  }

  fun updateFailedBlockStatusWithException(ex: Throwable) {
    log.error(ex) { "Internal error when contacting Azure DevOps" }
    updateFailedBlockStatusWithMessage(
      "There was an internal error while trying to contact Azure DevOps: ${ex.message}",
    )
  }

  fun updateFailedBlockStatusWithMessage(message: String) {
    log.error { "Error in block run $blockRunUuid: $message" }
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = AzureDevOpsBlockRunnerService.STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
          userMessage = "Could not trigger the Azure DevOps Pipeline",
          systemMessage = message,
        ),
      ),
    )
    blockRunClient.updateBlockRun(update)
  }

  fun updateSuccessfulTriggerStepStatus(
    pipelineRun: PipelineRun,
    isAsync: Boolean,
  ) {
    val extraPollingInformation = if (!isAsync) {
      "Polling for completion status..."
    } else {
      "Will wait for API updates on status..."
    }
    val webUrl = pipelineRun.links?.get("web")?.get("href") ?: pipelineRun.url ?: "N/A"
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = AzureDevOpsBlockRunnerService.STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
          userMessage = "Triggered Azure DevOps Pipeline. $extraPollingInformation",
          systemMessage = "Triggered pipeline run ${pipelineRun.id}. View run: $webUrl. $extraPollingInformation",
        ),
      ),
    )
    log.info {
      "Successfully triggered Azure DevOps pipeline ${pipelineRun.id} for run: $blockRunUuid, starting status polling"
    }
    blockRunClient.updateBlockRun(update)
  }

  fun updatePipelineStatusDuringPolling(pipelineRun: PipelineRun) {
    val userMessage = AzureDevOpsStatusMapper.mapPipelineStateToUserMessage(pipelineRun.state)
    val webUrl = pipelineRun.links?.get("web")?.get("href") ?: pipelineRun.url ?: "N/A"
    val systemMessage = "Pipeline run ${pipelineRun.id} state: ${pipelineRun.state.value}. View run: $webUrl"
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = AzureDevOpsBlockRunnerService.STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
          userMessage = userMessage,
          systemMessage = systemMessage,
        ),
      ),
    )
    log.debug { "Azure DevOps pipeline ${pipelineRun.id} state changed to '${pipelineRun.state.value}' for run: $blockRunUuid" }
    blockRunClient.updateBlockRun(update)
  }

  fun updatePipelineAndStageStatuses(
    pipelineRun: PipelineRun,
    timelineRecords: List<TimelineRecord>,
    reportedStages: MutableSet<String>,
  ) {
    val stages = timelineRecords.filter { record ->
      record.type == TimelineRecordType.STAGE && record.parentId == null
    }.sortedBy { it.order }
    if (stages.isEmpty()) {
      updatePipelineStatusDuringPolling(pipelineRun)
      return
    }
    val webUrl = pipelineRun.links?.get("web")?.get("href") ?: pipelineRun.url ?: "N/A"
    val stepsToUpdate = mutableListOf<MeshBuildingBlockRun.SourceUpdate.StepUpdate>()
    stepsToUpdate.add(
      MeshBuildingBlockRun.SourceUpdate.StepUpdate(
        id = AzureDevOpsBlockRunnerService.STEP_ID,
        status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
        userMessage = "Triggered Azure DevOps Pipeline",
        systemMessage = "Pipeline run ${pipelineRun.id}. View run: $webUrl",
      ),
    )
    for (stage in stages) {
      val stageId = stage.id
      val stageName = stage.name ?: "Unknown Stage"
      if (!reportedStages.contains(stageId) || stage.state == TimelineRecordState.COMPLETED) {
        val status = AzureDevOpsStatusMapper.mapStageToStatus(stage.state, stage.result)
        val userMessage = AzureDevOpsStatusMapper.mapStageToUserMessage(stageName, stage.state, stage.result)
        val systemMessage = AzureDevOpsStatusMapper.buildStageSystemMessage(
          stageName,
          stage.state,
          stage.result,
          stage.startTime,
          stage.finishTime,
        )
        stepsToUpdate.add(
          MeshBuildingBlockRun.SourceUpdate.StepUpdate(
            id = "ado-stage-$stageId",
            displayName = "Stage: $stageName",
            status = status,
            userMessage = userMessage,
            systemMessage = systemMessage,
          ),
        )
        reportedStages.add(stageId)
      }
    }
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
      steps = stepsToUpdate,
    )
    log.debug { "Updating stages for run $blockRunUuid: ${stages.map { it.name }}" }
    blockRunClient.updateBlockRun(update)
  }
}
