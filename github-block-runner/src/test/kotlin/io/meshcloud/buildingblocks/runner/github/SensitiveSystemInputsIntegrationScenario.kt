package io.meshcloud.buildingblocks.runner.github

import com.github.tomakehurst.wiremock.client.WireMock.*
import io.meshcloud.buildingblocks.runner.BlockRunnerService
import io.meshcloud.buildingblocks.runner.github.fixtures.TestAppTokenFactory
import io.meshcloud.buildingblocks.runner.github.fixtures.TestBlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.github.fixtures.TestDecryptionService
import io.meshcloud.buildingblocks.runner.github.fixtures.TestGitHubClientFactory
import io.meshcloud.buildingblocks.runner.github.fixtures.TestPrivateKeyProvider
import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGithubImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.test.context.ActiveProfiles
import org.springframework.test.context.TestPropertySource

/**
 * Integration test scenario for verifying that sensitive system inputs (MESHSTACK_API_TOKEN and MESHSTACK_RUN_TOKEN)
 * are correctly extracted from decrypted block run inputs and passed as workflow inputs to GitHub.
 *
 * This test spins up the full Spring context and uses WireMock to mock GitHub API responses.
 */
@SpringBootTest
@ActiveProfiles("test")
@TestPropertySource(
  properties = [
    "blockrunner.privateKey=test-private-key",
    "blockrunner.uuid=dc8c57a1-823f-4e96-8582-0275fa27dc7b",
    "blockrunner.auth.username=bb-api",
    "blockrunner.auth.password=guest",
    "blockrunner.api.url=http://localhost:8080",
  ],
)
@Import(SensitiveSystemInputsIntegrationScenario.TestConfig::class)
class SensitiveSystemInputsIntegrationScenario : WiremockTestBase() {

  @TestConfiguration
  class TestConfig {

    @Bean
    @Primary
    fun testDecryptionService(): DecryptionService = TestDecryptionService()

    @Bean
    @Primary
    fun testBlockRunClientFetcher(): BlockRunClientFetcher = TestBlockRunClientFetcher()

    @Bean
    @Primary
    fun testGitHubClientFactory(): GitHubClientFactory = TestGitHubClientFactory()

    @Bean
    @Primary
    fun testAppTokenFactory(): AppTokenFactory = TestAppTokenFactory()

    @Bean
    @Primary
    fun testCryptoConfig(): DecryptionService.PrivateKeyProvider = TestPrivateKeyProvider()
  }

  @Autowired
  private lateinit var blockRunnerService: BlockRunnerService

  @Autowired
  private lateinit var testBlockRunClientFetcher: TestBlockRunClientFetcher

  @BeforeEach
  fun setup() {
    wireMockServer.resetAll()
    testBlockRunClientFetcher.capturedUpdates.clear()
    testBlockRunClientFetcher.blockRunToReturn = null
  }

  @Test
  fun `sensitive system inputs are passed as workflow inputs when omitRunObjectInput is true`() {
    // Arrange: Create a block run with sensitive system inputs
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "encrypted:test-api-token-value",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY,
        value = "encrypted:test-run-token-value",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockInputsForRun(
        key = "SOME_OTHER_INPUT",
        value = "other-value",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = true, // Modern mode: pass URL + sensitive system inputs
    )

    val runUrl = buildingBlockRunUrl("1a9ad3bd-7457-4243-ab71-8e9701d331e1")
    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = runUrl)),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun

    // Stub GitHub API calls
    stubGitHubInstallationEndpoints()

    // Stub the workflow dispatch call and capture the request
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .withHeader("Authorization", equalTo("Bearer test-installation-token"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Note: ImmediateRetryDecorator always returns null, so we verify the workflow was triggered
    // by checking WireMock for the expected request instead of checking the return value.

    // Verify the workflow was triggered with the correct inputs
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    assertThat(requestBody).contains("\"buildingBlockRunUrl\"")
    assertThat(requestBody).contains(runUrl)

    // Verify decrypted sensitive system inputs are passed
    assertThat(requestBody).contains("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}\"")
    assertThat(requestBody).contains("test-api-token-value") // Decrypted value
    assertThat(requestBody).contains("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY}\"")
    assertThat(requestBody).contains("test-run-token-value") // Decrypted value

    // Verify other inputs are NOT passed directly (they should be fetched via URL)
    assertThat(requestBody).doesNotContain("SOME_OTHER_INPUT")
    assertThat(requestBody).doesNotContain("other-value")
  }

  @Test
  fun `only MESHSTACK_API_TOKEN is passed when MESHSTACK_RUN_TOKEN is not present`() {
    // Arrange: Create a block run with only MESHSTACK_API_TOKEN
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "encrypted:api-token-only",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
    )

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = true,
    )

    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = buildingBlockRunUrl("8h9ij0k5-eb2e-b9ba-hi48-f5je78ka08l8"))),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun
    stubGitHubInstallationEndpoints()
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Assert
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    assertThat(requestBody).contains("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}\"")
    assertThat(requestBody).contains("api-token-only")
    assertThat(requestBody).doesNotContain(BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY)
  }

  @Test
  fun `no sensitive system inputs are passed when none are present`() {
    // Arrange: Create a block run without sensitive system inputs
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = "REGULAR_INPUT",
        value = "regular-value",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = true,
    )

    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = buildingBlockRunUrl("9i0jk1l6-fc3f-caCb-ij59-g6kf89lb19m9"))),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun
    stubGitHubInstallationEndpoints()
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Assert
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    assertThat(requestBody).contains("\"buildingBlockRunUrl\"")
    // Only URL should be in inputs, no sensitive tokens
    assertThat(requestBody).doesNotContain(BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY)
    assertThat(requestBody).doesNotContain(BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY)
  }

  @Test
  fun `sensitive system inputs are NOT passed when omitRunObjectInput is false`() {
    // Arrange: Legacy mode - only the run object should be passed
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "encrypted:should-not-be-separate-input",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
    )

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = false, // Legacy mode: pass full run object
    )

    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = buildingBlockRunUrl("0j1kl2m7-gd4g-dbDc-jk60-h7lg90mc20n0"))),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun
    stubGitHubInstallationEndpoints()
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Assert
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    // In legacy mode, the run object is passed as base64
    assertThat(requestBody).contains("\"buildingBlockRun\"")
    // URL should NOT be a separate input
    assertThat(requestBody).doesNotContain("\"buildingBlockRunUrl\"")
    // Sensitive tokens should NOT be separate inputs (they're embedded in the run object)
    assertThat(requestBody).doesNotContain("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}\"")
    assertThat(requestBody).doesNotContain("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY}\"")
  }

  @Test
  fun `MESHSTACK_ENDPOINT is passed when MESHSTACK_API_TOKEN is present`() {
    // Arrange: Create a block run with API token and endpoint
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
        value = "encrypted:test-api-token",
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

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = true,
    )

    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = buildingBlockRunUrl("1k2lm3n8-he5h-ecEd-kl71-i8mh01nd31o1"))),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun
    stubGitHubInstallationEndpoints()
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Assert
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    assertThat(requestBody).contains("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}\"")
    assertThat(requestBody).contains("test-api-token")
    assertThat(requestBody).contains("\"${BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY}\"")
    assertThat(requestBody).contains("https://meshstack.example.com")
  }

  @Test
  fun `MESHSTACK_ENDPOINT is not passed when MESHSTACK_API_TOKEN is absent`() {
    // Arrange: Create a block run with only endpoint but no API token
    val inputs = listOf(
      MeshBuildingBlockInputsForRun(
        key = BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY,
        value = "https://meshstack.example.com",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
    )

    val implementation = MeshBuildingBlockGithubImplementation.test(
      async = true,
      omitRunObjectInput = true,
    )

    val blockRun = ProcessableBlockRun.test(
      implementation = implementation,
      inputs = inputs,
      links = mapOf("self" to HalLink(href = buildingBlockRunUrl("2l3mn4o9-if6i-fdFe-lm82-j9ni12oe42p2"))),
    )

    testBlockRunClientFetcher.blockRunToReturn = blockRun
    stubGitHubInstallationEndpoints()
    wireMockServer.stubFor(
      post(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches"))
        .willReturn(noContent()),
    )

    // Act
    blockRunnerService.processBlock()

    // Assert
    val requests = wireMockServer.findAll(
      postRequestedFor(urlPathEqualTo("/repos/owner/repository/actions/workflows/provision.yml/dispatches")),
    )
    assertThat(requests).hasSize(1)

    val requestBody = requests.first().bodyAsString
    assertThat(requestBody).contains("\"buildingBlockRunUrl\"")
    // Endpoint should NOT be passed without API token
    assertThat(requestBody).doesNotContain(BuildingBlockWorkflowInputsBuilder.MESHSTACK_ENDPOINT_KEY)
  }

  private fun stubGitHubInstallationEndpoints() {
    // Stub get installation ID
    wireMockServer.stubFor(
      get(urlPathEqualTo("/repos/owner/repository/installation"))
        .willReturn(
          okJson(
            """
                    {
                      "id": "12345",
                      "app_id": "app-id",
                      "client_id": "client-id",
                      "target_type": "Repository"
                    }
            """.trimIndent(),
          ),
        ),
    )

    // Stub get installation auth token
    wireMockServer.stubFor(
      post(urlPathEqualTo("/app/installations/12345/access_tokens"))
        .willReturn(
          created().withBody(
            """
                    {
                      "token": "test-installation-token",
                      "expires_at": "2025-12-31T23:59:59Z",
                      "permissions": {"actions": "write", "metadata": "read"},
                      "repository_selection": "all"
                    }
            """.trimIndent(),
          ),
        ),
    )
  }

  companion object {
    private const val TEST_BASE_URL = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns"

    private fun buildingBlockRunUrl(uuid: String) = "$TEST_BASE_URL/$uuid"
  }
}
