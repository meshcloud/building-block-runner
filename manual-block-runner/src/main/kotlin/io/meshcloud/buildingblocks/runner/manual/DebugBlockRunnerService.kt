package io.meshcloud.buildingblocks.runner.manual

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

private val log = KotlinLogging.logger { }

/**
 * This is a helper for development, and it will simulate randomly failing runs and post
 * some step updates with nonsense.
 */
class DebugBlockRunnerService(
  blockRunClientFetcher: BlockRunClientFetcher,
) : NoOpBlockRunnerService(blockRunClientFetcher) {

  init {
    log.warn { "Loaded DEBUG BlockRunnerService" }
  }

  override fun updateBlockStatus(blockRunClient: BlockRunClient) {
    val waitDelayMs = 5000L

    val blockRun = blockRunClient.activeBlockRun.meshObject
    val pendingUpdate = makeUpdate(blockRun, MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS, false)
    log("Update to RUNNING, sleeping 5s", blockRun.metadata.uuid)
    blockRunClient.updateBlockRun(pendingUpdate)

    Thread.sleep(waitDelayMs)

    val firstLogUpdate = makeUpdate(blockRun, MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS, false)
    log("Update first logs, sleeping 5s", blockRun.metadata.uuid)
    blockRunClient.updateBlockRun(firstLogUpdate)

    Thread.sleep(waitDelayMs)

    val secondLogUpdate = makeUpdate(blockRun, MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS, false)
    log("Update second logs, sleeping 5s", blockRun.metadata.uuid)
    blockRunClient.updateBlockRun(secondLogUpdate)

    Thread.sleep(waitDelayMs)

    val r = Math.random()
    val randomSuccessOrFail = if (r < 0.5) {
      MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED
    } else {
      MeshBuildingBlockRun.ExecutionStatus.FAILED
    }
    val finalUpdate = makeUpdate(blockRun, randomSuccessOrFail, true)

    log.info {
      "Updating Block ID: ${blockRun.metadata.uuid}, status: ${finalUpdate.status}, " +
        "variables: ${finalUpdate.steps?.mapNotNull { it.outputs }}, outputs: ${finalUpdate.steps?.firstOrNull { it.id == "debugStep" }?.outputs}"
    }

    blockRunClient.updateBlockRun(finalUpdate)
  }

  private fun log(stage: String, blockRunUuid: String) {
    log.info { "Updating Block ID: $blockRunUuid, stage: $stage" }
  }

  private fun makeUpdate(
    blockRun: MeshBuildingBlockRun,
    status: MeshBuildingBlockRun.ExecutionStatus,
    isLastStep: Boolean,
  ): MeshBuildingBlockRun.SourceUpdate {
    val outputs = blockRun.spec.buildingBlock.spec.inputs.associate {
      it.key to MeshBuildingBlockRun.SourceUpdate.StepUpdate.BlockRunOutput(
        value = it.value,
        type = it.type,
        isSensitive = it.isSensitive,
      )
    }

    return MeshBuildingBlockRun.SourceUpdate(
      status = status,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          userMessage = "this is a message for the user",
          systemMessage = "this is a message for the system",
          status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
        ),
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = "additionalDebugStep",
          userMessage = "this is a message for the user",
          systemMessage = "this is a message for the system",
          status = if (isLastStep) {
            MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED
          } else {
            MeshBuildingBlockRun.ExecutionStatus.PENDING
          },
          outputs = if (isLastStep) {
            outputs
          } else {
            null
          },
        ),
      ),
    )
  }
}
