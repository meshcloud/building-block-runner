package io.meshcloud.http

import okhttp3.MediaType.Companion.toMediaType

object MediaTypes {
  val MEDIA_TYPE_FORM = "application/x-www-form-urlencoded".toMediaType()
  val MEDIA_TYPE_JSON = "application/json".toMediaType()
  val MEDIA_TYPE_YAML = "application/yaml".toMediaType()
}
