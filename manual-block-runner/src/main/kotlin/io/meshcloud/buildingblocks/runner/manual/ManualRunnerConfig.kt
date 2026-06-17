package io.meshcloud.buildingblocks.runner.manual

import org.springframework.boot.context.properties.ConfigurationProperties

@ConfigurationProperties(prefix = "blockrunner")
data class ManualRunnerConfig(
  val debugMode: Boolean,
)
