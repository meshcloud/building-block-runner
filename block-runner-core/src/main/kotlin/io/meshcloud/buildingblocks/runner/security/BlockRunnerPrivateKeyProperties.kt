package io.meshcloud.buildingblocks.runner.security

import org.springframework.boot.context.properties.ConfigurationProperties

@ConfigurationProperties(prefix = "blockrunner")
class BlockRunnerPrivateKeyProperties {
  var privateKey: String = ""
  var privateKeyFile: String = ""
}
