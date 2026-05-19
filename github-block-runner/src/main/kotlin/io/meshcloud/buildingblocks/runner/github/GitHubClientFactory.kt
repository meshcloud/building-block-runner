package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import org.springframework.stereotype.Component

@Component
class GitHubClientFactory(
  private val urlSanitizer: UrlSanitizerService
) {
  fun provideClientFor(githubApiBaseUrl: String): GithubClient {
    val sanitizedUrl = urlSanitizer.sanitize(githubApiBaseUrl)

    return GithubClient(sanitizedUrl)
  }
}
