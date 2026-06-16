package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test

class UrlSanitizerServiceTest {

  private lateinit var sut: UrlSanitizerService

  @BeforeEach
  fun setUp() {
    sut = UrlSanitizerService()
  }

  @Test
  fun `sanitize removes trailing slash`() {
    val input = "https://api.github.com/api/v4/"
    val expected = "https://api.github.com/api/v4"

    val result = sut.sanitize(input)

    assertEquals(expected, result)
  }

  @Test
  fun `sanitize preserves URL without trailing slash`() {
    val input = "https://api.github.com"
    val expected = "https://api.github.com"

    val result = sut.sanitize(input)

    assertEquals(expected, result)
  }

  @Test
  fun `sanitize trims whitespace`() {
    val input = "  https://gitlab.example.com  "
    val expected = "https://gitlab.example.com"

    val result = sut.sanitize(input)

    assertEquals(expected, result)
  }


  @Test
  fun `sanitize throws exception for empty URL`() {
    val exception = assertThrows(IllegalArgumentException::class.java) {
      sut.sanitize("")
    }

    assertEquals("URL should not be empty", exception.message)
  }

  @Test
  fun `sanitize throws exception for whitespace-only URL`() {
    val exception = assertThrows(IllegalArgumentException::class.java) {
      sut.sanitize("   ")
    }

    assertEquals("URL should not be empty", exception.message)
  }
}
