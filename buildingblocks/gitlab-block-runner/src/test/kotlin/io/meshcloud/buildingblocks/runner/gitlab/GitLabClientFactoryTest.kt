package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.UrlSanitizerService
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test

class GitLabClientFactoryTest {

  private lateinit var sut: GitLabClientFactory
  private lateinit var urlSanitizer: UrlSanitizerService

  @Before
  fun setUp() {
    urlSanitizer = mockk()
    sut = GitLabClientFactory(urlSanitizer)
  }

  @Test
  fun `provideClientFor sanitizes URL before creating client`() {
    val inputUrl = "https://gitlab.example.com/"
    val sanitizedUrl = "https://gitlab.example.com"

    every { urlSanitizer.sanitize(inputUrl) } returns sanitizedUrl

    sut.provideClientFor(inputUrl)

    verify(exactly = 1) { urlSanitizer.sanitize(inputUrl) }
  }

}
