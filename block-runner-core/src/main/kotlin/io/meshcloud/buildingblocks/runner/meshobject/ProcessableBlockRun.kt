package io.meshcloud.buildingblocks.runner.meshobject

import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.annotation.JsonUnwrapped
import io.meshcloud.meshobjects.objects.*

/**
 * This is transformable back into a JSON object matching what came from our API. This can be handy if you
 * send this payload to an external system like the github runner does. But its quite ugly we have this special
 * solution here instead of a generic solution which can handle de- and serialization of meshObjects with additional
 * properties. Without a custom deserializer this is probably not working.
 */
data class ProcessableBlockRun(
  @JsonUnwrapped
  val meshObject: MeshBuildingBlockRun,
  @JsonUnwrapped
  @JsonProperty("_links")
  val links: Map<String, HalLink>,
) {
  fun selfLink(): String {
    return extractLinkStr("self")
  }

  fun registerSourceLink(): String {
    return extractLinkStr("registerSource")
  }

  fun updateSourceLink(): String {
    return extractLinkStr("updateSource")
  }

  private fun extractLinkStr(key: String): String {
    return links[key]?.href
      ?: throw IllegalStateException("No $key link present in the provided _links property")
  }

  companion object {
    fun test(
      implementation: BuildingBlockImplementation,
      inputs: List<MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun> = emptyList(),
      links: Map<String, HalLink> = mapOf(),
    ): ProcessableBlockRun {
      return ProcessableBlockRun(
        meshObject = MeshBuildingBlockRun(
          metadata = MeshBuildingBlockRun.MeshBuildingBlockRunMetadata(
            uuid = "test",
          ),
          spec = MeshBuildingBlockRun.MeshBuildingBlockRunSpec(
            runNumber = 1L,
            behavior = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.Behavior.APPLY,
            buildingBlock = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.BuildingBlock(
              uuid = "test",
              spec = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.BuildingBlock.MeshBuildingBlockSpecForRun(
                displayName = "name",
                workspaceIdentifier = "workspace",
                projectIdentifier = "project",
                fullPlatformIdentifier = "platform",
                inputs = inputs,
                parentBuildingBlocks = emptyList(),
              ),
            ),
            buildingBlockDefinition = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.BuildingBlockDefinition(
              uuid = "test",
              spec = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.BuildingBlockDefinition.MeshBuildingBlockMinimalDefinitionSpec(
                workspaceIdentifier = "test-workspace",
                version = 1L,
                implementation = implementation,
              ),
            ),
            runToken = "test",
          ),
          status = MeshBuildingBlockRun.RunStatus.IN_PROGRESS,
        ),
        links = links,
      )
    }
  }
}
