package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Before
import org.junit.Test

class GitHubClientFactoryTest {

  private lateinit var sut: GitHubClientFactory

  private lateinit var urlSanitizer: UrlSanitizerService

  @Before
  fun setUp() {
    urlSanitizer = mockk()
    sut = GitHubClientFactory(urlSanitizer)
  }

  @Test
  fun `provideClientFor sanitizes URL before creating client`() {
    val inputUrl = "https://api.github.com/"
    val sanitizedUrl = "https://api.github.com"

    every { urlSanitizer.sanitize(inputUrl) } returns sanitizedUrl

    sut.provideClientFor(inputUrl)

    verify(exactly = 1) { urlSanitizer.sanitize(inputUrl) }
  }
}
