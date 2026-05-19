package io.meshcloud.http

import io.meshcloud.exception.MeshSystemException
import io.meshcloud.http.HttpResponseBase.Companion.MAX_LOG_BODY_CHARS
import io.meshcloud.http.HttpResponseBase.Companion.consume
import io.meshcloud.http.HttpResponseBase.Companion.parseTypicalRestApiResponseBody
import io.meshcloud.http.HttpResponseBase.Companion.truncateBodyForLog
import io.mockk.every
import io.mockk.spyk
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.*
import org.junit.Test
import java.io.InputStreamReader

class HttpResponseBaseTest {
  @Test
  fun `test parseTypicalRestApiResponseBody with empty response`() {
    val response = mockResponse("")
    assertEquals("", parseTypicalRestApiResponseBody(response))
  }

  @Test
  fun `test parseTypicalRestApiResponseBody with response within limit`() {
    val responseBody = "{'key': 'value'}" // Response within 2MB
    val response = mockResponse(responseBody)
    assertEquals(responseBody, parseTypicalRestApiResponseBody(response))
  }

  @Test
  fun `test parseTypicalRestApiResponseBody with response exceeding limit`() {
    val largeResponse = "a".repeat(MAX_REST_RESPONSE_SIZE_CHARS + 1) // Response larger than 2MB
    val response: Response = mockResponse(largeResponse)

    assertThrows(MeshSystemException::class.java) {
      parseTypicalRestApiResponseBody(response)
    }
  }

  /**
   * This test case fail when you run it via IntelliJ, or in general in any other way than gradle. In gradle we already
   * provide a JVM argument to open up JDK internals. This test does a spyk on an InputStreamReader.
   * This mocking is only possible, when we open up the JDK internals. You can do so by adding
   * "--add-opens java.base/java.io=ALL-UNNAMED" to your jvmArgs when running this test. Or you run it via gradle, where
   * this is already configured in the gradle file.
   */
  @Test
  fun `test consume deals with a reader that behaves like a network stream`() {
    val totalChars = 100
    val charsPerRead = 10

    val largeResponse = "a".repeat(totalChars)
    val response: Response = mockResponse(largeResponse)

    val reader = InputStreamReader(response.body!!.byteStream())

    // we fake an input stream that only ever returns less than requested
    var calls = 0
    val spy = spyk(reader)
    every { spy.read(any(), any(), any()) } answers {
      calls++

      val (buffer, off, len) = args
      assertEquals(totalChars, len)
      reader.read(buffer as CharArray, off as Int, charsPerRead)
    }

    val (sb, hasMore) = consume(spy, totalChars)

    assertFalse(hasMore)
    assertEquals(largeResponse, sb.toString())

    val emptyReadCallAtEndOfStream = 1
    assertEquals(10 + emptyReadCallAtEndOfStream, calls)
  }


  @Test
  fun `truncateBodyForLog should return the same string when length is less than or equal to 100`() {
    val input = "This is a short string."
    assertEquals(input, truncateBodyForLog(input))
  }

  @Test
  fun `truncateBodyForLog should truncate string and append 'truncated' when length is greater than 100`() {
    val input = "a".repeat(MAX_LOG_BODY_CHARS + 1)

    val expected = "a".repeat(MAX_LOG_BODY_CHARS) + "<truncated>"
    assertEquals(expected, truncateBodyForLog(input))
  }

  @Test
  fun `truncateBodyForLog should handle empty string`() {
    assertEquals("", truncateBodyForLog(""))
  }

  @Test
  fun `toString does not render sensitive headers`() {
    val response = mockResponse("abc")
    val sut = HttpResponseSuccess.fromRestApiResponse<Any>(response)

    val result = sut.toString()
    val expected = "HttpResponseSuccess(request: GET http://mockurl/, httpStatus: OK, body: abc)"

    assertEquals(expected, result)
  }

  private val sensitive = "sensitive"

  private fun mockResponse(body: String): Response {
    val request = Request.Builder()
      .url("http://mockurl")
      .addHeader("Authorization", sensitive)
      .build()

    return Response.Builder()
      .code(200)
      .message("OK")
      .body(body.toResponseBody())
      .addHeader("SecretResponse", "notSecret")
      .protocol(Protocol.HTTP_1_1)
      .request(request)
      .build()
  }
}

