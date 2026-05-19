package io.meshcloud.buildingblocks.runner.runclient

interface BlockRunClientFetcher {
  fun fetchBlockRunClient(): BlockRunClient?
}
