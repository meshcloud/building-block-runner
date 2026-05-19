package io.meshcloud.buildingblocks.runner.manual

import io.meshcloud.buildingblocks.runner.SingleShotRunner
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFactory
import io.meshcloud.buildingblocks.runner.runclient.EnvironmentVariableProvider
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import mu.KotlinLogging
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.stereotype.Component
import org.springframework.test.context.ActiveProfiles
import java.io.File

private val log = KotlinLogging.logger {}

/**
 * Kubernetes profile manual runner scenario tests.
 * Provides a shared a customized BlockRunnerService where the KubernetesBlockRunClient receives a run JSON
 * file via its environmentVariableProvider override. This simulates providing the RUN_JSON_FILE_PATH
 * environment variable pointing to a temp file containing the run spec.
 * In contrast to the other runners this scenario here is a bit more extensive and also tests the API interaction
 * while all other runner tests only check if a Kubernetes based startup is possible.
 */
@SpringBootTest
@ActiveProfiles("kubernetes")
@Import(ManualRunnerKubernetesStartupScenario.KubernetesTestConfig::class)
class ManualRunnerKubernetesStartupScenario {

  @TestConfiguration
  class KubernetesTestConfig {
    @Component
    @Primary
    class TestRunTerminator : SingleShotRunner.RunTerminator {
      override fun exit(exitCode: Int) {
        log.info { "Exit with code: $exitCode" }
      }
    }

    @Component
    class TestBlockRunClient : BlockRunClient {
      private val registrations = mutableListOf<SourceRegistration>()
      private val updates = mutableListOf<SourceUpdateCapture>()

      override lateinit var activeBlockRun: ProcessableBlockRun

      override fun registerAsSource(stepId: String, stepDisplayName: String) {
        registrations.add(SourceRegistration(stepId, stepDisplayName))
        log.info { "Captured registerAsSource: stepId=$stepId, stepDisplayName=$stepDisplayName" }
      }

      override fun updateBlockRun(sourceUpdate: MeshBuildingBlockRun.SourceUpdate) {
        updates.add(SourceUpdateCapture(sourceUpdate))
        log.info { "Captured updateBlockRun: status=${sourceUpdate.status}, steps=${sourceUpdate.steps?.size}" }
      }

      fun getRegistrations(): List<SourceRegistration> = registrations.toList()

      fun getUpdates(): List<SourceUpdateCapture> = updates.toList()

      fun reset() {
        registrations.clear()
        updates.clear()
      }

      data class SourceRegistration(
        val stepId: String,
        val stepDisplayName: String
      )

      data class SourceUpdateCapture(
        val update: MeshBuildingBlockRun.SourceUpdate
      )
    }

    /**
     * This overrides the regular HttpRunTokenRunClientFactory so we can inject our TestBlockRunClient which
     * as handy methods to check for API interactions.
     */
    @Bean
    @Primary
    fun modifiedHttpRunTokenRunClientFactory(testBlockRunClient: TestBlockRunClient): BlockRunClientFactory {
      return object : BlockRunClientFactory {
        override fun buildBlockRunClient(run: ProcessableBlockRun): BlockRunClient {
          // inject the generated processable run into the client.
          testBlockRunClient.activeBlockRun = run

          return testBlockRunClient
        }
      }
    }

    @Component
    @Primary
    class TestVariableProvider : EnvironmentVariableProvider {
      private val tempFile = File.createTempFile("run-", ".json").apply {
        deleteOnExit()
        writeText(SAMPLE_RUN_JSON)
      }

      override fun fetchVariable(variableName: String): String? {
        return if (variableName == "RUN_JSON_FILE_PATH") tempFile.absolutePath else System.getenv(variableName)
      }
    }
  }

  @Autowired
  private lateinit var testBlockRunClient: KubernetesTestConfig.TestBlockRunClient

  @Test
  fun `spring boot can start the app and makes expected API calls`() {
    // Verify that the source was registered
    val registrations = testBlockRunClient.getRegistrations()
    assertThat(registrations).hasSize(1)
    assertThat(registrations[0].stepId).isEqualTo("manual")
    assertThat(registrations[0].stepDisplayName).isEqualTo("Manual Block Run")

    // Verify that the block run was updated with success status
    val updates = testBlockRunClient.getUpdates()
    assertThat(updates).hasSize(1)

    val update = updates[0].update
    assertThat(update.status).isEqualTo(MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED)
    assertThat(update.steps).hasSize(1)

    val step = update.steps!![0]
    assertThat(step.id).isEqualTo("manual")
    assertThat(step.status).isEqualTo(MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED)

    // Verify that outputs match the inputs from the sample JSON
    assertThat(step.outputs).isNotEmpty
    assertThat(step.outputs).containsKeys("test")
    assertThat(step.outputs!!["test"]?.value).isEqualTo("sss")
    assertThat(step.outputs!!["test"]?.type).isEqualTo(MeshBuildingBlockIOType.STRING)
    assertThat(step.outputs!!["test"]?.isSensitive).isFalse()
  }

  companion object {
    /** Sample JSON payload for testing. This is encoded to base64 at runtime. */
    val SAMPLE_RUN_JSON = """
      {
        "kind": "meshBuildingBlockRun",
        "apiVersion": "v1",
        "metadata": {
          "uuid": "c662eb0d-2ee2-4dc3-92df-d0b8bd41df44"
        },
        "spec": {
          "runToken": "aaaa-bbbb-cccc-dddd",
          "runNumber": 1,
          "buildingBlock": {
            "uuid": "23cf7c1b-f0ba-4139-a98c-b8cea37e83cf",
            "spec": {
              "displayName": "testmanualsss",
              "workspaceIdentifier": "managed-customer",
              "inputs": [
                {
                  "key": "test",
                  "value": "sss",
                  "type": "STRING",
                  "isSensitive": false,
                  "isEnvironment": false
                },
                {
                  "key": "test",
                  "value": "sss",
                  "type": "STRING",
                  "isSensitive": false,
                  "isEnvironment": false
                }
              ],
              "parentBuildingBlocks": []
            }
          },
          "buildingBlockDefinition": {
            "uuid": "f3228cf4-acde-4ff3-a0a5-1720b69d5bcd",
            "spec": {
              "version": 1,
              "workspaceIdentifier": "test-workspace",
              "implementation": {
                "type": "MANUAL"
              }
            }
          },
          "behavior": "APPLY"
        },
        "status": "IN_PROGRESS",
        "_links": {
          "self": {
            "href": "http://localhost:8080/api/meshobjects/meshbuildingblockruns/c662eb0d-2ee2-4dc3-92df-d0b8bd41df44"
          },
          "registerSource": {
            "href": "http://localhost:8080/api/meshobjects/meshbuildingblockruns/c662eb0d-2ee2-4dc3-92df-d0b8bd41df44/status/source"
          },
          "updateSource": {
            "href": "http://localhost:8080/api/meshobjects/meshbuildingblockruns/c662eb0d-2ee2-4dc3-92df-d0b8bd41df44/status/source/{sourceId}",
            "templated": true
          },
          "meshstackBaseUrl": {
            "href": "http://localhost:8080"
          }
        }
      }
    """.trimIndent()
  }
}
