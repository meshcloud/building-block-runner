package io.meshcloud.buildingblocks.runner.manual

import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.context.properties.ConfigurationPropertiesScan
import org.springframework.boot.runApplication
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

@ConfigurationPropertiesScan(basePackages = [
  "io.meshcloud.buildingblocks.runner",
])
@SpringBootApplication(scanBasePackages = [
  "io.meshcloud.buildingblocks.runner"
])
class BlockRunnerApplication

@Configuration
class ManualRunnerCryptoConfig {
  // The manual runner does not decrypt secrets itself; this placeholder keeps the
  // non-kubernetes Spring context bootstrappable when MeshCertDecryptionService is present.
  @Bean
  fun privateKeyProvider(): DecryptionService.PrivateKeyProvider {
    return object : DecryptionService.PrivateKeyProvider {
      override val privateKey: String = ""
    }
  }
}

fun main(args: Array<String>) {
  runApplication<BlockRunnerApplication>(*args)
}
