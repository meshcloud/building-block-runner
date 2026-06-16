package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import io.mockk.every
import io.mockk.impl.annotations.MockK
import io.mockk.junit5.MockKExtension
import io.mockk.verify
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.extension.ExtendWith

@ExtendWith(MockKExtension::class)
class GitHubClientFactoryTest {

  @MockK
  private lateinit var urlSanitizer: UrlSanitizerService

  private lateinit var sut: GitHubClientFactory

  @BeforeEach
  fun setUp() {
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
