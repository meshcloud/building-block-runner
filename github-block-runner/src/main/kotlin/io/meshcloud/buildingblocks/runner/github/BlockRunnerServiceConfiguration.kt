package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.*
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Profile

@Configuration
class BlockRunnerServiceConfiguration(
  private val blockRunClientFetcher: BlockRunClientFetcher,
) {

  @Bean
  @Profile("!kubernetes")
  fun blockRunnerService(
    gitHubClientFactory: GitHubClientFactory,
    decryptionService: DecryptionService,
    appTokenFactory: AppTokenFactory,
  ): BlockRunnerService {
    val service = GithubBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      gitHubClientFactory = gitHubClientFactory,
      decryptionService = decryptionService,
      appTokenFactory = appTokenFactory,
    )

    return ImmediateRetryDecorator(service)
  }

  @Bean
  @Profile("kubernetes")
  fun kubernetesBlockRunnerService(
    gitHubClientFactory: GitHubClientFactory,
    decryptionService: DecryptionService,
    appTokenFactory: AppTokenFactory,
  ): BlockRunnerService {
    return GithubBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      gitHubClientFactory = gitHubClientFactory,
      decryptionService = decryptionService,
      appTokenFactory = appTokenFactory,
    )
  }
}
