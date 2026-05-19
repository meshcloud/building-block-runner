@file:Suppress("RedundantOverride") // not redundant, see comments below

package io.meshcloud.http

import io.meshcloud.exception.MeshSystemException
import io.meshcloud.http.exception.MeshHttpException
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.Reader
import java.lang.StringBuilder
import kotlin.math.min

val ONE_MB_SIZE = 1024 * 1024

/**
 * A typical REST response should stay well within 2 MiB.
 * We therefore configure 4 MiB to be on the safe side as we initially roll out enforcement
 * of this limit, we can still consider making it tighter in production.
 *
 * We throw an exception to protect system availability - it's very easy to go OOM with large responses.
 * If you hit this limit, you should build an optimized implementation for an expensive API call.
 * It's very easy to end up allocating hundreds of MiB of memory to parse a megabyte-sized response with Jackson.
 *
 * Note: technically one char != one byte, so this is not fully accurate
 */
val MAX_REST_RESPONSE_SIZE_CHARS = 4 * ONE_MB_SIZE

/**
 * DESIGN:
 *
 * Many http client implementations try to be "helpful" by putting payload deserialization and error handling
 * into interceptors. For our use cases of http clients in meshStack, we found that to be a leaky abstraction
 * that adds more issues than it helps
 *
 * - we do a lot of API calls that are expected to fail with different error codes, e.g. 409 conflicts, 404 not found
 *   etc.
 * - the exact semantics of what constitutes an "error" is dependent on the context that we make the request from
 *   and in many cases we need to handle those status codes explicitly
 * - how we need to deserialize data can be different from API to API (e.g. different date formats required by
 *   different endpoints)
 * - different responses might need to deserialize response bodies to different data types
 *
 * HttpClients that use "conventions" and "interceptors" would throw exceptions for these use cases, but this
 * makes control flow very hard to follow and difficult to get right (e.g. ensure errors are always handled)l
 * An additional challenge is that for our use case in meshStack we want to carefully control which information
 * we expose to end users (semantic information what went wrong) vs. system operators (useful info for e.g. fixing
 * permission errors).
 *
 * This design powered by [OkHttpClient.execute] solves this problem by using a discriminated union to represent
 * response processing and giving callers explicit control over the process. While it may require a bit more boilerplate
 * for writing each API call, this boilerplate is simple and all error handling is explicit. This allows us to more
 * easily verify correctness.
 *
 * Please note that this design should eventually replace the legacy [HttpClient]
 *
 * Some usage tips:
 * - authentication is still an acceptable use of interceptors
 * - use OkHttpClient client directly in the facades and use [PayloadSerializer] to generate the actual content which
 * are now not bound to the client anymore. The Facade/Service can decide how the content has to be
 * serialized on a per-request basis. Object mapper can thus be reused easier (which saves
 * resources).
 *
 * File and class can be renamed to HttpResponse when the old [HttpResponse] is gone.
 */
sealed class HttpResponseBase<T> {
  abstract val request: Request
  abstract val status: HttpStatus
  abstract val responseHeaders: Map<String, List<String>>
  abstract val body: String

  fun handled(result: T): HttpResponseProcessed<T> {
    return HttpResponseProcessed(
      request = request,
      status = status,
      responseHeaders = responseHeaders,
      body = body,
      content = result
    )
  }

  override fun toString(): String {
    // do not include headers as they contain sensitive info
    return "${javaClass.simpleName}(request: ${request.method} ${request.url}, httpStatus: $status, body: ${truncateBodyForLog(body)})"
  }

  companion object {
    /**
     * We need to enforce a limit here in order to prevent denial of service/OOM issues.
     * This is important because we deal with untrusted input from 3rd party systems and will also
     * output this data to logs, UI etc.
     */
    fun parseTypicalRestApiResponseBody(response: Response): String {
      val body = response.body
        ?: return ""


      body.charStream().use { reader ->
        val (content, hasMore) = consume(reader, MAX_REST_RESPONSE_SIZE_CHARS)

        if (hasMore) {
          throw MeshSystemException(
            "Attempted to process a response for ${response.request.method} ${response.request.url} larger than $MAX_REST_RESPONSE_SIZE_CHARS chars. This is not supported by the http client infrastructure and requires an implementation designed to use streaming."
          )
        }

        return content.toString()
      }
    }

    /**
     * Attempts to read the maximum amount of bytes from the stream.
     * @return true if has more, false if not
     */
    fun consume(reader: Reader, maxChars: Int): Pair<StringBuilder, Boolean> {
      val stringBuilder = StringBuilder()
      val buffer = CharArray(1024)

      while (stringBuilder.length <= maxChars) {
        val charsRead = reader.read(buffer, 0, min(buffer.size, maxChars))

        val endOfStream = charsRead == -1
        if (endOfStream) {
          return stringBuilder to false
        }

        stringBuilder.appendRange(buffer, 0, charsRead)
      }

      return stringBuilder to true
    }

    /**
     * We must enforce a sensible limit on the size of logs and messages passed to meshPanel
     * e.g. as part of replication logs. Empirically, this should be enough to capture most API error responses
     * that we need to show on a UI.
     */
    const val MAX_LOG_BODY_CHARS = 2048

    fun truncateBodyForLog(input: String): String {
      return if (input.length <= MAX_LOG_BODY_CHARS) {
        input
      } else {
        input.substring(0, MAX_LOG_BODY_CHARS) + "<truncated>"
      }
    }
  }
}

data class HttpResponseSuccess<T>(
  override val request: Request,
  override val status: HttpStatus,
  override val responseHeaders: Map<String, List<String>>,
  override val body: String
) : HttpResponseBase<T>() {

  // ensure we call the sanitized base class version, as kotlin's generates toString methods for data classes that print
  // all fields verbatim
  override fun toString(): String {
    return super.toString()
  }

  companion object {
    fun <T> fromRestApiResponse(response: Response): HttpResponseSuccess<T> {
      return HttpResponseSuccess(
        request = response.request,
        status = HttpStatus.valueOf(response.code),
        responseHeaders = response.headers.toMultimap(),
        body = parseTypicalRestApiResponseBody(response)
      )
    }
  }
}

data class HttpResponseFailed<T>(
  override val request: Request,
  override val status: HttpStatus,
  override val responseHeaders: Map<String, List<String>>,
  override val body: String
) : HttpResponseBase<T>() {
  // ensure we call the sanitized base class version, as kotlin's generates toString methods for data classes that print
  // all fields verbatim
  override fun toString(): String {
    return super.toString()
  }

  companion object {
    fun <T> fromRestApiResponse(response: Response): HttpResponseFailed<T> {
      return HttpResponseFailed(
        request = response.request,
        status = HttpStatus.valueOf(response.code),
        responseHeaders = response.headers.toMultimap(),
        body = parseTypicalRestApiResponseBody(response)
      )
    }

    fun test(status: HttpStatus, body: String = "error"): HttpResponseFailed<Any> {
      return HttpResponseFailed(
        request = Request.Builder().url("http://example.com").build(),
        body = body,
        responseHeaders = emptyMap(),
        status = status
      )
    }
  }
}

data class HttpResponseProcessed<T>(
  override val request: Request,
  override val status: HttpStatus,
  override val responseHeaders: Map<String, List<String>>,
  override val body: String,
  val content: T
) : HttpResponseBase<T>() {
  // ensure we call the sanitized base class version, as kotlin's generates toString methods for data classes that print
  // all fields verbatim
  override fun toString(): String {
    return super.toString()
  }
}

/**
 * Helper functions to make handling of responses even easier.
 * All the 2xx family is considered a success.
 */
inline fun <reified T> HttpResponseBase<T>.letIfSuccess(
  fn: (HttpResponseBase<T>) -> HttpResponseBase<T>
): HttpResponseBase<T> {
  if (this is HttpResponseProcessed) {
    return this
  }

  if (this is HttpResponseSuccess || this.status.is2xxSuccessful()) {
    return fn(this)
  }

  return this
}

inline fun <reified T> HttpResponseBase<T>.letIfError(
  fn: (HttpResponseFailed<T>) -> HttpResponseBase<T>
): HttpResponseBase<T> {
  return when (this) {
    is HttpResponseProcessed -> this
    is HttpResponseFailed -> fn(this)
    else -> this
  }
}

inline fun <reified T> HttpResponseBase<T>.getContent(): T {
  return when (this) {
    is HttpResponseProcessed -> this.content

    else -> throw MeshHttpException(
      systemMessage = "Unexpected HTTP error invoking a remote API", // all basic request/response info is added by the exception constructor
      userMessage = "There was an error talking to a remote system when processing the request. " +
        "Please contact support if the problem persists.",
      response = this
    )
  }
}

inline fun <reified T> HttpResponseBase<T>.letWithStatus(
  vararg status: HttpStatus,
  fn: (HttpResponseBase<T>) -> HttpResponseBase<T>
): HttpResponseBase<T> {
  if (this is HttpResponseProcessed) {
    return this
  }

  if (status.toSet().contains(this.status)) {
    return fn(this)
  }

  return this
}
