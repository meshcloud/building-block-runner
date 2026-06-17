package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGithubImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.Test

class BuildingBlockWorkflowInputsBuilderTest {

  companion object {
    private const val TEST_BUILDING_BLOCK_RUN_URL = "https://meshstack.company.com/api/meshobjects/meshbuildingblockruns/1a9ad3bd-7457-4243-ab71-8e9701d331e1"
  }

  @Test
  fun `WithUrl includes buildingBlockRunUrl`() {
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
  }

  @Test
  fun `WithUrl includes MESHSTACK_API_TOKEN when available`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "test-api-token",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY, "test-api-token")
  }

  @Test
  fun `WithUrl includes MESHSTACK_RUN_TOKEN when available`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY,
        value = "test-run-token",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY, "test-run-token")
  }

  @Test
  fun `WithUrl does not include other sensitive inputs`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = "SOME_OTHER_SECRET",
        value = "secret-value",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).hasSize(1)
    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).doesNotContainKey("SOME_OTHER_SECRET")
  }

  @Test
  fun `WithUrl includes MESHSTACK_ENDPOINT when MESHSTACK_API_TOKEN is present`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "test-api-token",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY,
        value = "https://meshstack.example.com",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).hasSize(3)
    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY, "test-api-token")
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY, "https://meshstack.example.com")
  }

  @Test
  fun `WithUrl does not include MESHSTACK_ENDPOINT when MESHSTACK_API_TOKEN is absent`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY,
        value = "https://meshstack.example.com",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).hasSize(1)
    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).doesNotContainKey(BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY)
  }

  @Test
  fun `WithUrl includes all system inputs when available`() {
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "test-api-token",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY,
        value = "test-run-token",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY,
        value = "https://meshstack.example.com",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )
    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      inputs = inputs,
      links = mapOf("self" to HalLink(href = TEST_BUILDING_BLOCK_RUN_URL)),
    )

    val inputMap = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap()

    assertThat(inputMap).hasSize(4)
    assertThat(inputMap).containsEntry("buildingBlockRunUrl", TEST_BUILDING_BLOCK_RUN_URL)
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY, "test-api-token")
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY, "test-run-token")
    assertThat(inputMap).containsEntry(BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY, "https://meshstack.example.com")
  }
}
