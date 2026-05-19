package io.meshcloud.buildingblocks.runner

import org.springframework.stereotype.Service

@Service
class UrlSanitizerService {

  fun sanitize(url: String): String {
    val trimmedUrl = url.trim()

    if (trimmedUrl.isEmpty()) {
      throw IllegalArgumentException("URL should not be empty")
    }

    return if (trimmedUrl.endsWith("/")) {
      trimmedUrl.dropLast(1)
    } else {
      trimmedUrl
    }
  }
}
