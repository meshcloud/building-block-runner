package io.meshcloud.buildingblocks.runner.azuredevops

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClient
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRun
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRunState
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import java.time.Clock
import java.time.Duration
import java.time.Instant

private val log = KotlinLogging.logger { }

object AzureDevOpsPipelinePoller {
  /**
   * This method will keep polling the Azure DevOps pipeline run status until it reaches a final state and
   * keep updating the status of the building block run accordingly.
   */
  fun pollPipelineCompletion(
    azureDevOpsClient: AzureDevOpsClient,
    statusUpdater: AzureDevOpsStatusUpdater,
    blockRun: MeshBuildingBlockRun,
    pipelineRun: PipelineRun,
    clock: Clock = Clock.systemUTC(),
  ) {
    val triggerTime = Instant.now(clock)
    log.info { "Starting pipeline status polling for run: ${blockRun.metadata.uuid}, pipeline run: ${pipelineRun.id}" }

    try {
      var currentRun = pipelineRun
      val maxPollingDuration = Duration.ofMinutes(MAX_POLLING_MINUTES_PIPELINES.toLong())
      var lastReportedState: String? = null
      val reportedStages = mutableSetOf<String>()

      while (!isPipelineRunComplete(currentRun)) {
        if (Instant.now(clock).isAfter(triggerTime.plus(maxPollingDuration))) {
          statusUpdater.updateFailedBlockStatusWithException(
            Exception("Pipeline polling timeout after $MAX_POLLING_MINUTES_PIPELINES minutes"),
          )
          return
        }

        Thread.sleep(POLLING_INTERVAL_SECONDS * 1000L)

        try {
          currentRun = azureDevOpsClient.getPipelineRun(currentRun.id)

          log.debug { "Pipeline run ${currentRun.id} state: ${currentRun.state}, result: ${currentRun.result}" }

          try {
            val timelineRecords = azureDevOpsClient.getPipelineTimeline(currentRun.id)
            statusUpdater.updatePipelineAndStageStatuses(currentRun, timelineRecords, reportedStages)
          } catch (ex: Exception) {
            log.warn(ex) { "Failed to get timeline records, will use basic status update" }
            if (lastReportedState != currentRun.state.value) {
              statusUpdater.updatePipelineStatusDuringPolling(currentRun)
              lastReportedState = currentRun.state.value
            }
          }
          lastReportedState = currentRun.state.value
        } catch (ex: Exception) {
          log.warn(ex) { "Failed to get pipeline run status, will retry" }
          continue
        }
      }
      statusUpdater.updateFinalBlockStatusFromPipeline(currentRun)
    } catch (ex: Exception) {
      log.error(ex) { "Error during pipeline polling" }
      statusUpdater.updateFailedBlockStatusWithException(ex)
    }
  }

  private fun isPipelineRunComplete(pipelineRun: PipelineRun): Boolean {
    return pipelineRun.state == PipelineRunState.COMPLETED
  }

  private const val MAX_POLLING_MINUTES_PIPELINES = 30 // Maximum time in minutes to poll for pipelines
  private const val POLLING_INTERVAL_SECONDS = 10 // How long to wait between polling attempts
}
