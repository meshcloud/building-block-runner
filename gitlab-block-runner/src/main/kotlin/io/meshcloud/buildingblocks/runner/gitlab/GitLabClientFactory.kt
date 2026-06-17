package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import org.springframework.stereotype.Component

/**
 * Mainly to allow injection of the client during tests.
 */
@Component
class GitLabClientFactory(
  private val urlSanitizer: UrlSanitizerService,
) {
  fun provideClientFor(gitlabApiBaseUrl: String): GitLabClient {
    val sanitizedUrl = urlSanitizer.sanitize(gitlabApiBaseUrl)

    return GitLabClient(sanitizedUrl)
  }
}
