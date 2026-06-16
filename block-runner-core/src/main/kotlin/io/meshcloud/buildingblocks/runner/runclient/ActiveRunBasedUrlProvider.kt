package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun


class ActiveRunBasedUrlProvider(
  private val activeBlockRun: ProcessableBlockRun,
  private val config: BlockRunnerApiConfig,
) : UrlProvider {
  override fun getRegisterSourceUrl(): String {
    return activeBlockRun.registerSourceLink()
  }

  override fun getUpdateSourceUrl(): String {
    // The HAL link is a URI template with a {sourceId} variable, e.g.:
    // .../meshbuildingblockruns/{uuid}/status/source/{sourceId}
    val template = activeBlockRun.updateSourceLink()

    require(template.contains("{sourceId}")) {
      "Expected HAL template to contain '{sourceId}' placeholder, but got '$template' (config.uuid=${config.uuid})"
    }

    return template.replace("{sourceId}", config.uuid)
  }
}
