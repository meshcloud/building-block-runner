package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClient
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRun
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRunResult
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRunState
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.MeshStackRejectedRequestException
import io.meshcloud.buildingblocks.runner.runclient.MeshStackRequestOutcomeUnknownException
import io.meshcloud.meshobjects.objects.MeshBuildingBlockAzureDevOpsImplementation
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.Test
import java.io.IOException

class AzureDevOpsPipelinePollerTest {

  private val azureDevOpsClient: AzureDevOpsClient = mockk()
  private val statusUpdater: AzureDevOpsStatusUpdater = mockk(relaxed = true)
  private val completedPipelineRun = PipelineRun(
    id = 1L,
    name = "pipeline",
    state = PipelineRunState.COMPLETED,
    result = PipelineRunResult.SUCCEEDED,
    createdDate = "2026-10-08T12:00:00Z",
    finishedDate = "2026-10-08T12:05:00Z",
    url = null,
    links = null,
  )

  @Test
  fun `a final status that may not have reached meshStack ends the run without reporting an Azure DevOps error`() {
    every { statusUpdater.updateFinalBlockStatusFromPipeline(any()) } throws
      MeshStackRequestOutcomeUnknownException("meshStack did not take the status update", IOException("Connection refused"))

    assertThatThrownBy { poll() }.isInstanceOf(MeshStackRequestOutcomeUnknownException::class.java)

    verify(exactly = 0) { statusUpdater.updateFailedBlockStatusWithException(any()) }
  }

  @Test
  fun `a final status that meshStack rejects is reported as a meshStack rejection`() {
    val rejection = MeshStackRejectedRequestException(400, "meshStack answered HTTP 400: invalid output")
    every { statusUpdater.updateFinalBlockStatusFromPipeline(any()) } throws rejection

    poll()

    verify { statusUpdater.updateFailedBlockStatusWithRejectedStatusUpdate(rejection) }
    verify(exactly = 0) { statusUpdater.updateFailedBlockStatusWithException(any()) }
  }

  @Test
  fun `a lost stage status update is not sent again as a basic status update, and the final status still follows`() {
    every { azureDevOpsClient.getPipelineRun(1L) } returns completedPipelineRun
    every { azureDevOpsClient.getPipelineTimeline(1L) } returns emptyList()
    every { statusUpdater.updatePipelineAndStageStatuses(any(), any(), any()) } throws
      MeshStackRequestOutcomeUnknownException("meshStack did not take the status update", IOException("Connection refused"))

    poll(completedPipelineRun.copy(state = PipelineRunState.IN_PROGRESS, result = null))

    verify(exactly = 0) { statusUpdater.updatePipelineStatusDuringPolling(any()) }
    verify { statusUpdater.updateFinalBlockStatusFromPipeline(completedPipelineRun) }
  }

  private fun poll(pipelineRun: PipelineRun = completedPipelineRun) = AzureDevOpsPipelinePoller.pollPipelineCompletion(
    azureDevOpsClient = azureDevOpsClient,
    statusUpdater = statusUpdater,
    blockRun = ProcessableBlockRun.test(implementation = MeshBuildingBlockAzureDevOpsImplementation.test()).meshObject,
    pipelineRun = pipelineRun,
  )
}
