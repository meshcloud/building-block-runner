package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.security.BlockRunnerPrivateKeyProperties
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.buildingblocks.runner.security.PrivateKeyLoader
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

@Configuration
class GitLabBlockRunnerCryptoConfiguration {
  @Bean
  fun privateKeyProvider(props: BlockRunnerPrivateKeyProperties): DecryptionService.PrivateKeyProvider {
    val key = PrivateKeyLoader.resolve(props.privateKeyFile, props.privateKey)
    return object : DecryptionService.PrivateKeyProvider {
      override val privateKey: String = key
    }
  }
}
