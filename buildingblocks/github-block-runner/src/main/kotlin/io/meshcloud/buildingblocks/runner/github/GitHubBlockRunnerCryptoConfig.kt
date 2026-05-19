package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Profile

@ConfigurationProperties(prefix = "blockrunner")
@Profile("!kubernetes")
data class GitHubBlockRunnerCryptoConfig(
  override val privateKey: String
) : DecryptionService.PrivateKeyProvider
