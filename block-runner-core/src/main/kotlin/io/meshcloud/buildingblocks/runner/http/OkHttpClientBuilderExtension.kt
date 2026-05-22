package io.meshcloud.buildingblocks.runner.http

import io.github.oshai.kotlinlogging.KLogger
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor

fun OkHttpClient.Builder.addLogging(log: KLogger): OkHttpClient.Builder = apply {
  if (log.isTraceEnabled()) {
    val loggingInterceptor = HttpLoggingInterceptor().apply {
      redactHeader("Authorization")
      redactHeader("Cookie")
      level = HttpLoggingInterceptor.Level.BODY
    }
    addInterceptor(loggingInterceptor)
  }
}
