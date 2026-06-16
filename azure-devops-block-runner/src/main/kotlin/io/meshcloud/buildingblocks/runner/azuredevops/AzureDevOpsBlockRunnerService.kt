package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.BlockRunnerService
import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClientFactory
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.http.MeshHttpException
import io.meshcloud.meshobjects.objects.MeshBuildingBlockAzureDevOpsImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.github.oshai.kotlinlogging.KotlinLogging

private val log = KotlinLogging.logger { }

class AzureDevOpsBlockRunnerService(
  private val blockRunClientFetcher: BlockRunClientFetcher,
  private val azureDevOpsClientFactory: AzureDevOpsClientFactory
) : BlockRunnerService {

  override fun processBlock(): MeshBuildingBlockRun? {
    val blockRunClient = try {
      blockRunClientFetcher.fetchBlockRunClient()
    } catch (ex: Exception) {
      log.error(ex) { "Unexpected error while getting a block run." }
      null
    } ?: return null

    val blockRunWithLinks = blockRunClient.activeBlockRun
    val blockRun = blockRunWithLinks.meshObject
    val statusUpdater = AzureDevOpsStatusUpdater(blockRunClient, blockRun.metadata.uuid)

    blockRunClient.registerAsSource(
      STEP_ID,
      "Trigger Azure DevOps Pipeline"
    )

    val implementation = try {
      blockRun.getImplementation<MeshBuildingBlockAzureDevOpsImplementation>()
    } catch (ex: IllegalStateException) {
      statusUpdater.updateFailedBlockStatusWithException(ex)
      return null
    }

    val azureDevOpsClient = try {
      azureDevOpsClientFactory.provideClientFor(blockRunWithLinks)
    } catch (ex: Exception) {
      statusUpdater.updateFailedBlockStatusWithException(ex)
      return null
    }

    val pipelineRun = try {
      azureDevOpsClient.triggerPipeline()
    } catch (ex: MeshHttpException) {
      statusUpdater.updateFailedBlockStatusWithMeshException(ex)
      return null
    } catch (ex: Exception) {
      statusUpdater.updateFailedBlockStatusWithException(ex)
      return null
    }

    statusUpdater.updateSuccessfulTriggerStepStatus(pipelineRun, implementation.async)

    if (!implementation.async) {
      // For synchronous building blocks, poll for pipeline completion
      AzureDevOpsPipelinePoller.pollPipelineCompletion(
        azureDevOpsClient = azureDevOpsClient,
        blockRun = blockRun,
        pipelineRun = pipelineRun,
        statusUpdater = statusUpdater
      )
    }

    return blockRun
  }

  companion object {
    const val STEP_ID = "azure-devops-trigger"
  }
}
