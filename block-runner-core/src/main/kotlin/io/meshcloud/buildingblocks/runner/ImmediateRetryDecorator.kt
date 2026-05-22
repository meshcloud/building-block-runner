package io.meshcloud.buildingblocks.runner

import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.github.oshai.kotlinlogging.KotlinLogging

private val log = KotlinLogging.logger { }

/**
 * If a block was successfully processed, in order to speed up the processing we immediately
 * retry it without waiting for the external scheduler to call into the BlockRunnerService
 * again.
 */
class ImmediateRetryDecorator(
  private val wrappedService: BlockRunnerService
) : BlockRunnerService {
  override fun processBlock(): MeshBuildingBlockRun? {
    do {
      val updatedBlock = wrappedService.processBlock()
      if (updatedBlock != null) {
        log.debug { "Block was processed, immediately retry" }
      }
    } while (updatedBlock != null)

    return null
  }
}
