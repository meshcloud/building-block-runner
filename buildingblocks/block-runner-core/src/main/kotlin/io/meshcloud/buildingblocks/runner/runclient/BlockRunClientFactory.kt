package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun

interface BlockRunClientFactory {
  fun buildBlockRunClient(run: ProcessableBlockRun): BlockRunClient
}
