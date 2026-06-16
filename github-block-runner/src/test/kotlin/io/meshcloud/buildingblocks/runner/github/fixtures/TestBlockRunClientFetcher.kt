package io.meshcloud.buildingblocks.runner.github.fixtures

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

/**
 * Mutable BlockRunClientFetcher that allows tests to configure the block run to be processed.
 * It returns the block run only once, then returns null to prevent infinite loops in the ImmediateRetryDecorator.
 *
 * This fixture captures all block run updates for test assertions.
 */
class TestBlockRunClientFetcher : BlockRunClientFetcher {
  var blockRunToReturn: ProcessableBlockRun? = null
  val capturedUpdates = mutableListOf<MeshBuildingBlockRun.SourceUpdate>()

  override fun fetchBlockRunClient(): BlockRunClient? {
    val run = blockRunToReturn ?: return null
    // Clear after first fetch to prevent ImmediateRetryDecorator infinite loop
    blockRunToReturn = null
    return object : BlockRunClient {
      override val activeBlockRun: ProcessableBlockRun = run

      override fun registerAsSource(stepId: String, stepName: String) {
        // No-op for tests
      }

      override fun updateBlockRun(sourceUpdate: MeshBuildingBlockRun.SourceUpdate) {
        capturedUpdates.add(sourceUpdate)
      }
    }
  }
}
