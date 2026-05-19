package io.meshcloud.buildingblocks.runner

import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun

interface BlockRunnerService {

  fun processBlock(): MeshBuildingBlockRun?
}
