package io.meshcloud.buildingblocks.runner

import org.springframework.boot.context.properties.ConfigurationProperties

@ConfigurationProperties(prefix = "blockrunner")
data class BlockRunnerApiConfig(
  val uuid: String,
  val version: String,
)
