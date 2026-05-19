package io.meshcloud.http

import com.fasterxml.jackson.module.kotlin.readValue

/**
 * Represents a HttpResponse
 *
 * Note: This http response data class is necessary to react depend on empty responses.
 * This means when a PUT or DELETE call returns an empty response body then we can return null.
 */
data class HttpResponse<T>(
  val status: HttpStatus,
  val responseHeaders: Map<String, Any>,
  /**
   * A parsed response body. Only parsed for HttpStatus codes < 400
   */
  val responseBody: T?,
  /**
   * The unparsed response content for custom error handling. Set if the response did not match an explicitly defined
   * [HttpClient.ExpectedStatus]. If no explicit expect status was specified, this is set for response status codes > 400.
   */
  val errorContentAsString: String?,
  private val throwUnexpected: () -> Unit
) {

  /**
   * Allows consumers to read a customer error response type.
   * @see errorContentAsString
   */
  inline fun <reified T> readErrorResponse(): T {
    errorContentAsString ?: throw IllegalStateException("Response contains no unexpected content")
    return HttpClient.objectJsonMapper.readValue(errorContentAsString)
  }


  /**
   * Consumers can call this if they decide that the response is, for whatever reason, unexpected and they don't want
   * or can provide a way to handle it explicitly.
   *
   * Note: the return value here is so that it can be called from any callsite without tripping up the compiler
   * see https://stackoverflow.com/questions/15019662/is-it-possible-to-tell-the-compiler-that-a-method-always-throws-an-exception
   */
  fun <T> throwUnexpectedResponse(): T {
    this.throwUnexpected.invoke()
    throw IllegalStateException("did not throw")
  }
}
