package io.meshcloud.buildingblocks.runner.manual

import io.meshcloud.buildingblocks.runner.BlockRunnerService
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.github.oshai.kotlinlogging.KotlinLogging

private val log = KotlinLogging.logger { }

open class NoOpBlockRunnerService(
  private val blockRunClientFetcher: BlockRunClientFetcher
) : BlockRunnerService {

  override fun processBlock(): MeshBuildingBlockRun? {
    val blockRunClient = try {
      blockRunClientFetcher.fetchBlockRunClient()
    } catch (ex: Exception) {
      log.error(ex) { "Unexpected error while getting a block run." }

      null
    } ?: return null

    blockRunClient.registerAsSource(
      stepId = STEP_ID,
      stepDisplayName = "Manual Block Run"
    )

    updateBlockStatus(blockRunClient)

    return blockRunClient.activeBlockRun.meshObject
  }

  protected open fun updateBlockStatus(blockRunClient: BlockRunClient) {
    val inputs = blockRunClient
      .activeBlockRun
      .meshObject
      .spec
      .buildingBlock
      .spec
      .inputs.associateBy { it.key }

    val updateDto = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
          outputs = inputs.mapValues { (_, value) ->
            MeshBuildingBlockRun.SourceUpdate.StepUpdate.BlockRunOutput(
              value = value.value,
              type = toOutputType(value.type),
              isSensitive = value.isSensitive
            )
          }
        )
      ),
    )

    log.info {
      "Updating Block with runId: ${blockRunClient.activeBlockRun.meshObject.metadata.uuid}, status: ${updateDto.status}, " +
        "variables: $inputs, outputs: ${updateDto.steps!!.single().outputs}"
    }

    blockRunClient.updateBlockRun(updateDto)
  }

  companion object {
    @JvmStatic
    protected val STEP_ID = "manual"

    /**
     * Translates an input type to a supported output type.
     * Supported output types: STRING, INTEGER, BOOLEAN, CODE.
     */
    fun toOutputType(inputType: MeshBuildingBlockIOType): MeshBuildingBlockIOType {
      return when (inputType) {
        MeshBuildingBlockIOType.STRING -> MeshBuildingBlockIOType.STRING
        MeshBuildingBlockIOType.INTEGER -> MeshBuildingBlockIOType.INTEGER
        MeshBuildingBlockIOType.BOOLEAN -> MeshBuildingBlockIOType.BOOLEAN
        MeshBuildingBlockIOType.CODE -> MeshBuildingBlockIOType.CODE
        MeshBuildingBlockIOType.FILE -> MeshBuildingBlockIOType.STRING
        MeshBuildingBlockIOType.LIST -> MeshBuildingBlockIOType.CODE
        MeshBuildingBlockIOType.SINGLE_SELECT -> MeshBuildingBlockIOType.STRING
        MeshBuildingBlockIOType.MULTI_SELECT -> MeshBuildingBlockIOType.CODE
      }
    }
  }
}

