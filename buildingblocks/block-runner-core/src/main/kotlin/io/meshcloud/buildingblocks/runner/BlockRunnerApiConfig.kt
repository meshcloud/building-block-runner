package io.meshcloud.buildingblocks.runner

import org.springframework.boot.context.properties.ConfigurationProperties

@ConfigurationProperties(prefix = "blockrunner")
data class BlockRunnerApiConfig(
  val api: ApiConfig,
  val uuid: String,
  /**
   * You can omit the whole auth block to make configuration easier. This
   * means you will fall back to the usage of run tokens automatically.
   */
  val auth: BlockRunnerAuthConfig? = null
) {
  data class ApiConfig(
    val url: String
  )

  data class BlockRunnerAuthConfig(
    val username: String,
    val password: String
  )
}
