package io.meshcloud.http

import com.fasterxml.jackson.core.type.TypeReference
import com.fasterxml.jackson.databind.ObjectMapper
import io.meshcloud.http.exception.MeshHttpException
import okhttp3.MediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

/**
 * Helper class to especially easily deserialize response payloads with a reified helper
 * function.
 */
class PayloadSerializer(
  private val objectMapper: ObjectMapper
) {

  /**
   * Note: when using this method, please don't forget to set an appropriate content type on the request!
   */
  fun serialize(obj: Any, contentType: MediaType = MediaTypes.MEDIA_TYPE_JSON): RequestBody {
    return objectMapper.writeValueAsString(obj).toRequestBody(contentType)
  }

  inline fun <reified T> deserialize(
    response: HttpResponseBase<*>
  ): T? {
    val typeRef = object : TypeReference<T>() {}
    return deserialize(response, typeRef)
  }

  inline fun <reified T> deserializeOrThrow(
    response: HttpResponseBase<*>
  ): T {
    val typeRef = object : TypeReference<T>() {}

    return deserialize(response, typeRef)
      ?: throw MeshHttpException(
        userMessage = "The server response was unexpectedly empty. Please try again if and the error persist contact support.",
        systemMessage = "The server response was body was unexpectedly empty.",
        response = response
      )
  }

  fun <T> deserialize(
    response: HttpResponseBase<*>,
    typeReference: TypeReference<T>
  ): T? {
    try {
      return when {
        response.body.isEmpty() -> null
        else -> objectMapper.readValue(response.body, typeReference)
      }
    } catch (e: IOException) {
      // An own exception type would be better but for now probably only [MeshHttpExceptions] are
      // caught in the modules which use this. Better switch to the unified exception model.
      throw MeshHttpException(
        userMessage = "A remote server response could not be parsed. If the error persists please contact support.",
        systemMessage = e.message ?: "",
        response = response,
        cause = e
      )
    }
  }
}
