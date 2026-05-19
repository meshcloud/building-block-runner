package io.meshcloud.http.exception

import io.meshcloud.http.HttpResponseFailed
import io.meshcloud.http.HttpStatus
import okhttp3.Request
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class MeshHttpExceptionTest {

  private val secret = "1234-1234-secret"
  private val request = Request.Builder().url("http://example.com").build()

  @Test
  fun `Auth headers are removed from exception`() {
    val ex = MeshHttpException(
      systemMessage = "system",
      userMessage = "user",
      response = HttpResponseFailed<Any>(
        request = request,
        body = "body",
        responseHeaders = mapOf(
          "Authorization" to listOf(secret),
          "Proxy" to listOf("other")
        ),
        status = HttpStatus.NOT_FOUND
      )
    )

    assertEquals(listOf("***"), ex.response.responseHeaders["Authorization"])
  }

  @Test
  fun `Auth headers are removed when in different case`() {
    val ex = MeshHttpException(
      systemMessage = "system",
      userMessage = "user",
      response = HttpResponseFailed<Any>(
        request = request,
        body = "body",
        responseHeaders = mapOf(
          "AuTHORIZATION" to listOf(secret),
          "Proxy" to listOf("other")
        ),
        status = HttpStatus.NOT_FOUND
      )
    )

    assertEquals(listOf("***"), ex.response.responseHeaders["Authorization"])
  }

}
