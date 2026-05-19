package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.BlockRunnerService
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.http.exception.MeshHttpException
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGitlabImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import mu.KotlinLogging

private val log = KotlinLogging.logger { }

class GitLabBlockRunnerService(
  private val blockRunClientFetcher: BlockRunClientFetcher,
  private val gitlabClientFactory: GitLabClientFactory,
  private val decryptionService: DecryptionService,
) : BlockRunnerService {

  override fun processBlock(): MeshBuildingBlockRun? {
    val blockRunClient = try {
      blockRunClientFetcher.fetchBlockRunClient()
    } catch (ex: Exception) {
      log.error(ex) { "Unexpected error while getting a block run." }

      null
    } ?: return null

    val blockRun = blockRunClient.activeBlockRun.meshObject
    blockRunClient.registerAsSource(
      STEP_ID,
      "Trigger GitLab CI/CD"
    )

    val implementation = try {
      blockRun.getImplementation<MeshBuildingBlockGitlabImplementation>()
    } catch (ex: IllegalStateException) {
      updateFailedBlockStatusWithException(blockRunClient, ex)

      return null
    }

    val gitlabClient = try {
      gitlabClientFactory.provideClientFor(implementation.gitlabBaseUrl)
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)

      return null
    }

    try {
      gitlabClient.triggerPipeline(
        pipelineToken = decryptionService.decrypt(implementation.pipelineTriggerToken),
        refName = implementation.refName,
        projectId = implementation.projectId,
        run = decryptionService.decryptBlockRunInputs(blockRunClient.activeBlockRun)
      )
    } catch (ex: MeshHttpException) {
      updateFailedBlockStatusWithMeshException(blockRunClient, ex)

      return null
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)

      return null
    }

    updateToFinalBlockStatusForTrigger(blockRunClient, implementation.projectId)

    return blockRun
  }

  private fun updateFailedBlockStatusWithMeshException(blockRun: BlockRunClient, ex: MeshHttpException) {
    log.error(ex) { "Error contacting GitLab" }

    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
          userMessage = "Could not trigger the GitLab pipeline",
          systemMessage = "GitLab responded with status: ${ex.response.status} and body: ${ex.getResponseBody()}"
        )
      ),
    )

    blockRun.updateBlockRun(update)
  }

  private fun updateFailedBlockStatusWithException(blockRun: BlockRunClient, ex: Throwable) {
    log.error(ex) { "Internal error when contacting GitLab" }

    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
          userMessage = "Could not trigger the GitLab pipeline",
          systemMessage = "There was an internal error while trying to contact GitLab: ${ex.message}"
        )
      ),
    )

    blockRun.updateBlockRun(update)
  }

  private fun updateToFinalBlockStatusForTrigger(blockRun: BlockRunClient, projectId: String) {
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
          userMessage = "Triggered the configured GitLab pipeline",
          systemMessage = "Triggered pipeline in project '$projectId'"
        )
      ),
    )

    log.info {
      "Successfully triggered GitLab pipeline in project '$projectId' for run: ${blockRun.activeBlockRun.meshObject.metadata.uuid}"
    }
    blockRun.updateBlockRun(update)
  }

  companion object {
    @JvmStatic
    private val STEP_ID = "gl-trigger"
  }
}

