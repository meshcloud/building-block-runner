package io.meshcloud.buildingblocks.runner.runclient

import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component
import java.io.File

@Component
@Profile("kubernetes")
class RunFileJsonBlockRunClientFetcher(
  private val blockRunClientFactory: BlockRunClientFactory,
  private val environmentVariableProvider: EnvironmentVariableProvider,
  private val processableRunFactory: ProcessableRunFactory
) : BlockRunClientFetcher {

  override fun fetchBlockRunClient(): BlockRunClient? {
    // Extract the file path of the K8S secret file that is mounted.
    val runSpecFilePath = environmentVariableProvider
      .fetchVariable("RUN_JSON_FILE_PATH")
      ?: throw IllegalArgumentException("No env variable 'RUN_JSON_FILE_PATH' was provided")

    val runJson = File(runSpecFilePath).readText(Charsets.UTF_8)

    val meshRunWithLinks = processableRunFactory.buildProcessableRun(runJson)

    return blockRunClientFactory.buildBlockRunClient(meshRunWithLinks)
  }
}
