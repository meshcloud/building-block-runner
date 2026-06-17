package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.*
import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClientFactory
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Profile

@Configuration
class BlockRunnerServiceConfiguration(
  private val blockRunClientFetcher: BlockRunClientFetcher,
) {

  @Bean
  @Profile("!kubernetes")
  fun azureDevOpsBlockRunnerService(
    azureDevOpsClientFactory: AzureDevOpsClientFactory,
  ): BlockRunnerService {
    val service = AzureDevOpsBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      azureDevOpsClientFactory = azureDevOpsClientFactory,
    )

    return ImmediateRetryDecorator(service)
  }

  @Bean
  @Profile("kubernetes")
  fun kubernetesAzureDevOpsBlockRunnerService(
    azureDevOpsClientFactory: AzureDevOpsClientFactory,
  ): BlockRunnerService {
    return AzureDevOpsBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      azureDevOpsClientFactory = azureDevOpsClientFactory,
    )
  }
}
