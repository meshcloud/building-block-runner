package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

interface BlockRunClient {
  val activeBlockRun: ProcessableBlockRun

  fun registerAsSource(
    stepId: String,
    stepDisplayName: String,
  )

  fun updateBlockRun(
    sourceUpdate: MeshBuildingBlockRun.SourceUpdate,
  )
}
