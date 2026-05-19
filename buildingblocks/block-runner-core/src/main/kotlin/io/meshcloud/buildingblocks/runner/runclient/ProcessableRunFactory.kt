package io.meshcloud.buildingblocks.runner.runclient

import com.fasterxml.jackson.core.type.TypeReference
import com.fasterxml.jackson.databind.ObjectMapper
import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import org.springframework.stereotype.Component

@Component
class ProcessableRunFactory(
  private val objectMapper: ObjectMapper,
) {
  fun buildProcessableRun(runJson: String): ProcessableBlockRun {
    // Parse the same way as HttpBlockRunClient
    val run = objectMapper.readValue(runJson, MeshBuildingBlockRun::class.java)

    // Extract _links separately to avoid @JsonUnwrapped deserialization issues
    val linksOnly = objectMapper.readTree(runJson).get("_links").toString()
    val runLinks = objectMapper.readValue(linksOnly, object : TypeReference<Map<String, HalLink>>() {})

    // Combine them into ProcessableBlockRun
    return ProcessableBlockRun(
      meshObject = run,
      links = runLinks
    )
  }
}
