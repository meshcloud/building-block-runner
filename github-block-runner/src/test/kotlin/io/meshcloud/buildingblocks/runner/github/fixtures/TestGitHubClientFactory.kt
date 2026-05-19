package io.meshcloud.buildingblocks.runner.github.fixtures

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import io.meshcloud.buildingblocks.runner.github.GitHubClientFactory
import io.meshcloud.buildingblocks.runner.github.GithubClient
import io.meshcloud.buildingblocks.runner.github.WiremockTestBase

/**
 * Test GitHubClientFactory that always returns a client pointing to WireMock server.
 * This allows tests to intercept and verify GitHub API calls.
 */
class TestGitHubClientFactory : GitHubClientFactory(UrlSanitizerService()) {
  override fun provideClientFor(githubApiBaseUrl: String): GithubClient {
    return GithubClient(WiremockTestBase.BASE_URL)
  }
}
