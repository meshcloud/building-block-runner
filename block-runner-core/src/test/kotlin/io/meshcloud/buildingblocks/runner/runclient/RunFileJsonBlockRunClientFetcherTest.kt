package io.meshcloud.buildingblocks.runner.runclient

import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshManualBuildingBlockImplementation
import io.mockk.MockKAnnotations
import io.mockk.every
import io.mockk.impl.annotations.MockK
import io.mockk.verify
import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.io.File

class RunFileJsonBlockRunClientFetcherTest {

  @MockK
  private lateinit var blockRunClientFactory: BlockRunClientFactory

  @MockK
  private lateinit var blockRunClient: BlockRunClient

  @MockK
  private lateinit var environmentVariableProvider: EnvironmentVariableProvider

  @MockK
  private lateinit var processableRunFactory: ProcessableRunFactory

  @TempDir
  private lateinit var tempDir: File

  private val envVars = mutableMapOf<String, String?>()

  private val testObjectMapper = ObjectMapper()
    .apply {
      registerKotlinModule()
      registerModules(JavaTimeModule())
      configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
    }

  @BeforeEach
  fun setup() {
    MockKAnnotations.init(this, relaxUnitFun = true)

    every { environmentVariableProvider.fetchVariable(any()) } answers {
      envVars[arg(0)]
    }
  }

  @Test
  fun `fetchBlockRunClient throws exception when environment variable is not set`() {
    // Given: No environment variable set
    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When & Then
    assertThatThrownBy {
      sut.fetchBlockRunClient()
    }.isInstanceOf(IllegalArgumentException::class.java)
      .hasMessageContaining("RUN_JSON_FILE_PATH")
  }

  @Test
  fun `fetchBlockRunClient throws exception when environment variable is null`() {
    // Given
    envVars["RUN_JSON_FILE_PATH"] = null
    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When & Then
    assertThatThrownBy {
      sut.fetchBlockRunClient()
    }.isInstanceOf(IllegalArgumentException::class.java)
      .hasMessageContaining("RUN_JSON_FILE_PATH")
  }

  @Test
  fun `fetchBlockRunClient returns client when file contains valid JSON`() {
    // Given
    val testRun = ProcessableBlockRun.test(implementation = MeshManualBuildingBlockImplementation())
    val json = testObjectMapper.writeValueAsString(testRun)

    val runFile = File(tempDir, "run.json")
    runFile.writeText(json, Charsets.UTF_8)
    envVars["RUN_JSON_FILE_PATH"] = runFile.absolutePath

    every { processableRunFactory.buildProcessableRun(json) } returns testRun
    every { blockRunClientFactory.buildBlockRunClient(testRun) } returns blockRunClient

    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When
    val result = sut.fetchBlockRunClient()

    // Then
    assertThat(result).isSameAs(blockRunClient)
    verify(exactly = 1) { processableRunFactory.buildProcessableRun(json) }
    verify(exactly = 1) { blockRunClientFactory.buildBlockRunClient(testRun) }
  }

  @Test
  fun `fetchBlockRunClient correctly reads file with UTF-8 encoding`() {
    // Given
    val testRun = ProcessableBlockRun.test(implementation = MeshManualBuildingBlockImplementation())
    val json = testObjectMapper.writeValueAsString(testRun)

    val runFile = File(tempDir, "run.json")
    runFile.writeText(json, Charsets.UTF_8)
    envVars["RUN_JSON_FILE_PATH"] = runFile.absolutePath

    var capturedJson: String? = null
    every { processableRunFactory.buildProcessableRun(any()) } answers {
      capturedJson = firstArg()
      testRun
    }
    every { blockRunClientFactory.buildBlockRunClient(testRun) } returns blockRunClient

    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When
    sut.fetchBlockRunClient()

    // Then
    assertThat(capturedJson).isEqualTo(json)
  }

  @Test
  fun `fetchBlockRunClient delegates parsing to processableRunFactory`() {
    // Given
    val testRun = ProcessableBlockRun.test(implementation = MeshManualBuildingBlockImplementation())
    val json = testObjectMapper.writeValueAsString(testRun)

    val runFile = File(tempDir, "run.json")
    runFile.writeText(json, Charsets.UTF_8)
    envVars["RUN_JSON_FILE_PATH"] = runFile.absolutePath

    every { processableRunFactory.buildProcessableRun(json) } returns testRun
    every { blockRunClientFactory.buildBlockRunClient(testRun) } returns blockRunClient

    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When
    sut.fetchBlockRunClient()

    // Then
    verify(exactly = 1) { processableRunFactory.buildProcessableRun(json) }
  }

  @Test
  fun `fetchBlockRunClient passes processable run to factory`() {
    // Given
    val testRun = ProcessableBlockRun.test(implementation = MeshManualBuildingBlockImplementation())
    val json = testObjectMapper.writeValueAsString(testRun)

    val runFile = File(tempDir, "run.json")
    runFile.writeText(json, Charsets.UTF_8)
    envVars["RUN_JSON_FILE_PATH"] = runFile.absolutePath

    every { processableRunFactory.buildProcessableRun(json) } returns testRun

    var capturedRun: ProcessableBlockRun? = null
    every { blockRunClientFactory.buildBlockRunClient(any()) } answers {
      capturedRun = firstArg()
      blockRunClient
    }

    val sut = RunFileJsonBlockRunClientFetcher(
      blockRunClientFactory = blockRunClientFactory,
      environmentVariableProvider = environmentVariableProvider,
      processableRunFactory = processableRunFactory
    )

    // When
    sut.fetchBlockRunClient()

    // Then
    assertThat(capturedRun).isSameAs(testRun)
    assertThat(capturedRun?.meshObject?.metadata?.uuid).isEqualTo("test")
    assertThat(capturedRun?.meshObject?.spec?.runNumber).isEqualTo(1L)
  }
}

