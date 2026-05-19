package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Profile

@ConfigurationProperties(prefix = "blockrunner")
@Profile("!kubernetes")
data class GitLabBlockRunnerCryptoConfig(
  override val privateKey: String
) : DecryptionService.PrivateKeyProvider
