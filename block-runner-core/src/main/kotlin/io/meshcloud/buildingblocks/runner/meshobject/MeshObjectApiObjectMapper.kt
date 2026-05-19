package io.meshcloud.buildingblocks.runner.meshobject

import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.datatype.jdk8.Jdk8Module
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.registerKotlinModule

/**
 * A shared ObjectMapper that correctly serializes meshObject API objects.
 */
object MeshObjectApiObjectMapper {
  val mapper = jacksonObjectMapper()
    .registerKotlinModule()
    .registerModule(Jdk8Module())
    .registerModule(JavaTimeModule())
    .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
}
