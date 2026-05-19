package io.meshcloud.http

import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * This is the new improved variant of our HTTP client execution for standard REST APIs.
 * By separating the request execution from the client we can better control
 * error handling and deserialization.
 *
 * Please review [HttpResponseBase] for design details.
 */
inline fun <reified T> OkHttpClient.execute(request: Request): HttpResponseBase<T> {
  newCall(request)
    .execute()
    .use { response ->
      val httpStatus = HttpStatus.valueOf(response.code)

      val isFailed = !(httpStatus.is2xxSuccessful() || httpStatus.is3xxRedirection())

      return if (isFailed) {
        HttpResponseFailed.fromRestApiResponse(response)
      } else {
        HttpResponseSuccess.fromRestApiResponse(response)
      }
    }
}

