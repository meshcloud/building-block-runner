package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Profile

@ConfigurationProperties(prefix = "blockrunner")
@Profile("!kubernetes")
data class AzureDevOpsBlockRunnerCryptoConfig(
  override val privateKey: String
) : DecryptionService.PrivateKeyProvider
