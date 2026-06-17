package io.meshcloud.buildingblocks.runner.azuredevops.client

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.MeshBuildingBlockAzureDevOpsImplementation
import org.springframework.stereotype.Component

@Component
class AzureDevOpsClientFactory(
  private val decryptionService: DecryptionService,
) {

  fun provideClientFor(
    run: ProcessableBlockRun,
  ): AzureDevOpsClient {
    val implementation = run.meshObject.getImplementation<MeshBuildingBlockAzureDevOpsImplementation>()
    return AzureDevOpsClient(
      azureDevOpsBaseUrl = implementation.azureDevOpsBaseUrl,
      accessToken = decryptionService.decrypt(implementation.personalAccessToken),
      organization = implementation.organization,
      project = implementation.project,
      pipelineId = implementation.pipelineId,
      run = decryptionService.decryptBlockRunInputs(run),
      refName = implementation.refName,
    )
  }
}
