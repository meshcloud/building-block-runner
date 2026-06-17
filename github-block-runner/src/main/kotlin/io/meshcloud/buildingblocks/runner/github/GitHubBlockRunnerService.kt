package io.meshcloud.buildingblocks.runner.github

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.BlockRunnerService
import io.meshcloud.buildingblocks.runner.http.MeshHttpException
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGithubImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.format.DateTimeFormatter

private val log = KotlinLogging.logger { }

class GithubBlockRunnerService(
  private val blockRunClientFetcher: BlockRunClientFetcher,
  private val gitHubClientFactory: GitHubClientFactory,
  private val decryptionService: DecryptionService,
  private val appTokenFactory: AppTokenFactory,
  private val clock: Clock = Clock.systemUTC(),
) : BlockRunnerService {

  override fun processBlock(): MeshBuildingBlockRun? {
    val blockRunClient = try {
      blockRunClientFetcher.fetchBlockRunClient()
    } catch (ex: Exception) {
      log.error(ex) { "Unexpected error while getting a block run." }
      null
    } ?: return null

    val processableBlockRun = blockRunClient.activeBlockRun
    val blockRun = processableBlockRun.meshObject

    blockRunClient.registerAsSource(
      STEP_ID,
      "Trigger GitHub Action",
    )

    val implementation = try {
      blockRun.getImplementation<MeshBuildingBlockGithubImplementation>()
    } catch (ex: IllegalStateException) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    val githubClient = try {
      gitHubClientFactory.provideClientFor(
        githubApiBaseUrl = implementation.githubBaseUrl,
      )
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    val appAuthToken = try {
      val decryptedPem = decryptionService.decrypt(implementation.appPem)
      appTokenFactory.getAppAuthToken(
        appId = implementation.appId,
        appPem = decryptedPem,
      )
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    val installationId = try {
      githubClient.getInstallationId(
        appAuthToken = appAuthToken,
        owner = implementation.owner,
        repositoryName = implementation.repository,
      )
    } catch (ex: MeshHttpException) {
      updateFailedBlockStatusWithMeshException(blockRunClient, ex)
      return null
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    val installationAuthToken = try {
      githubClient.getInstallationAuthToken(
        appAuthToken = appAuthToken,
        installationId = installationId,
      )
    } catch (ex: MeshHttpException) {
      updateFailedBlockStatusWithMeshException(blockRunClient, ex)
      return null
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    val workflowName = when (blockRun.spec.behavior) {
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.Behavior.APPLY -> implementation.applyWorkflow
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.Behavior.DETECT -> implementation.applyWorkflow
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.Behavior.DESTROY -> implementation.destroyWorkflow
    }

    if (workflowName == null) {
      updateFailedBlockStatusWithMessage(
        blockRunClient,
        "Workflow file name must not be null",
      )
      return null
    }

    try {
      val triggerResult = triggerWorkflow(
        processableBlockRun,
        implementation,
        githubClient,
        installationAuthToken,
        workflowName,
      )

      when (triggerResult) {
        is GithubClient.TriggerWorkflowResult.Success -> {
          updateSuccessfulTriggerStepStatus(blockRunClient, workflowName, implementation.async)
        }

        is GithubClient.TriggerWorkflowResult.UnsupportedInput -> {
          // We know which inputs are unsupported - generate appropriate error messages
          val systemMessage = triggerResult.unsupportedInputNames.joinToString("\n") { inputName ->
            unsupportedInputSystemMessage(
              workflowName = workflowName,
              unsupportedInput = inputName,
              omitRunObjectInput = implementation.omitRunObjectInput,
            )
          }
          updateFailedBlockStatusWithMessage(blockRunClient, systemMessage)
          return null
        }

        is GithubClient.TriggerWorkflowResult.Error -> {
          updateFailedBlockStatusWithMessage(
            blockRunClient,
            "GitHub API returned status ${triggerResult.statusCode} when triggering workflow: ${triggerResult.responseBody}",
          )
          return null
        }
      }
    } catch (ex: Exception) {
      updateFailedBlockStatusWithException(blockRunClient, ex)
      return null
    }

    if (!implementation.async) {
      // For synchronous building blocks, poll for workflow completion with job tracking
      pollWorkflowCompletion(
        blockRunClient = blockRunClient,
        blockRun = blockRun,
        githubClient = githubClient,
        installationAuthToken = installationAuthToken,
        implementation = implementation,
        workflowName = workflowName,
      )
    }

    return blockRun
  }

  private fun triggerWorkflow(
    blockRun: ProcessableBlockRun,
    implementation: MeshBuildingBlockGithubImplementation,
    githubClient: GithubClient,
    installationAuthToken: String,
    workflowName: String,
  ): GithubClient.TriggerWorkflowResult {
    val decryptedBlockRun = decryptionService.decryptBlockRunInputs(blockRun)

    // Create the appropriate input builder based on the feature flag
    val inputBuilder = if (implementation.omitRunObjectInput) {
      // Only pass the URL and sensitive system inputs - workflow must fetch the rest (modern approach)
      BuildingBlockWorkflowInputsBuilder.WithUrl(decryptedBlockRun)
    } else {
      // Only pass the run object for legacy workflows
      BuildingBlockWorkflowInputsBuilder.WithRun(decryptedBlockRun)
    }

    // Build the input map and create the payload
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = implementation.branch,
      inputs = inputBuilder.toInputMap(),
    )

    // Determine which inputs we're sending that the workflow might not support
    // - When omitRunObjectInput=true: only sending URL and system tokens, old workflows may not support them
    // - When omitRunObjectInput=false: only sending run object, new workflows may not support it
    // We have to use this convoluted logic because GitHub doesn't provide structured error message and we have to
    // use some heuristic which error case we're dealing with. The GitHubClient will return us a
    // proper TriggerWorkflowResult.UnsupportedInput if it recognizes one of these inputs
    val recognizedUnsupportedInputs = setOf(
      "buildingBlockRunUrl",
      "buildingBlockRun",
      BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
      BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY,
    )

    val triggerResult = githubClient.triggerWorkflow(
      installationAuthToken = installationAuthToken,
      owner = implementation.owner,
      repositoryName = implementation.repository,
      workflowName = workflowName,
      payload = payload,
      recognizedUnsupportedInputs = recognizedUnsupportedInputs,
    )

    return triggerResult
  }

  private fun pollWorkflowCompletion(
    blockRunClient: BlockRunClient,
    blockRun: MeshBuildingBlockRun,
    githubClient: GithubClient,
    installationAuthToken: String,
    implementation: MeshBuildingBlockGithubImplementation,
    workflowName: String,
  ) {
    val triggerTime = Instant.now(clock)

    log.info { "Starting workflow and job status polling for run: ${blockRun.metadata.uuid}" }

    try {
      var workflowRun: GithubClient.WorkflowRun? = null
      var attempts = 0

      // First, try to find the workflow run that was triggered by our dispatch
      while (workflowRun == null && attempts < MAX_FIND_WORKFLOW_ATTEMPTS) {
        attempts++
        try {
          val recentRuns = githubClient.listWorkflowRuns(
            installationAuthToken = installationAuthToken,
            owner = implementation.owner,
            repositoryName = implementation.repository,
            workflowName = workflowName,
            perPage = 5,
          )

          // Find the most recent run that was created after we triggered the workflow
          workflowRun = recentRuns.firstOrNull { run ->
            val runCreatedAt = Instant.from(DateTimeFormatter.ISO_INSTANT.parse(run.createdAt))
            runCreatedAt.isAfter(triggerTime.minusSeconds(30)) // 30 second buffer
          }

          if (workflowRun == null) {
            log.debug { "Workflow run not found yet, attempt $attempts/$MAX_FIND_WORKFLOW_ATTEMPTS" }
            Thread.sleep(POLLING_INTERVAL_SECONDS * 1000L)
          }
        } catch (ex: Exception) {
          log.warn(ex) { "Failed to find workflow run on attempt $attempts" }
          Thread.sleep(POLLING_INTERVAL_SECONDS * 1000L)
        }
      }

      if (workflowRun == null) {
        updateFailedBlockStatusWithMessage(
          blockRunClient,
          "Could not find the triggered workflow run after $MAX_FIND_WORKFLOW_ATTEMPTS attempts",
        )
        return
      }

      log.info { "Found workflow run ${workflowRun.id} for block run ${blockRun.metadata.uuid}" }

      // Track jobs we've seen to detect new ones
      val seenJobIds = mutableSetOf<Long>()

      // Now poll the workflow run and its jobs until completion
      var currentRun = workflowRun
      val maxPollingDuration = Duration.ofMinutes(MAX_POLLING_MINUTES_WORKFLOWS.toLong())
      while (currentRun != null && !isWorkflowRunComplete(currentRun)) {
        if (Instant.now(clock).isAfter(triggerTime.plus(maxPollingDuration))) {
          updateFailedBlockStatusWithTimeout(blockRunClient)
          return
        }

        Thread.sleep(POLLING_INTERVAL_SECONDS * 1000L)

        try {
          // Get updated workflow run status
          currentRun = githubClient.getWorkflowRun(
            installationAuthToken = installationAuthToken,
            owner = implementation.owner,
            repositoryName = implementation.repository,
            runId = currentRun.id,
          )

          // Get jobs for this workflow run
          val jobs = githubClient.listWorkflowJobs(
            installationAuthToken = installationAuthToken,
            owner = implementation.owner,
            repositoryName = implementation.repository,
            runId = currentRun.id,
          )

          // Update job status for any new or updated jobs
          updateJobStatuses(blockRunClient, blockRun.metadata.uuid, jobs, seenJobIds)

          log.debug { "Workflow run ${currentRun.id} status: ${currentRun.status}, conclusion: ${currentRun.conclusion}, jobs: ${jobs.size}" }
        } catch (ex: Exception) {
          log.warn(ex) { "Failed to get workflow run status, will retry" }
          continue
        }
      }

      // Get final job statuses before updating final workflow status
      try {
        val finalJobs = githubClient.listWorkflowJobs(
          installationAuthToken = installationAuthToken,
          owner = implementation.owner,
          repositoryName = implementation.repository,
          runId = currentRun.id,
        )
        updateJobStatuses(blockRunClient, blockRun.metadata.uuid, finalJobs, seenJobIds)
      } catch (ex: Exception) {
        log.warn(ex) { "Failed to get final job statuses" }
      }

      // Update final status based on workflow conclusion
      updateFinalBlockStatusFromWorkflow(blockRunClient, blockRun.metadata.uuid, currentRun)
    } catch (ex: Exception) {
      log.error(ex) { "Error during workflow polling" }
      updateFailedBlockStatusWithException(blockRunClient, ex)
    }
  }

  private fun isWorkflowRunComplete(workflowRun: GithubClient.WorkflowRun?): Boolean {
    return workflowRun != null && workflowRun.status == GithubClient.WorkflowRunStatus.COMPLETED
  }

  private fun updateJobStatuses(
    blockRunClient: BlockRunClient,
    blockRunUuid: String,
    jobs: List<GithubClient.WorkflowJob>,
    seenJobIds: MutableSet<Long>,
  ) {
    val newOrUpdatedJobs = jobs.filter { job ->
      // Report all jobs that we haven't seen before or that have been updated
      val isNew = !seenJobIds.contains(job.id)
      if (isNew) {
        seenJobIds.add(job.id)
      }
      isNew || job.status == GithubClient.WorkflowJobStatus.COMPLETED
    }

    if (newOrUpdatedJobs.isNotEmpty()) {
      val stepUpdates = newOrUpdatedJobs.map { job ->
        val status = when {
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion == "success" -> MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion != "success" -> MeshBuildingBlockRun.ExecutionStatus.FAILED
          job.status == GithubClient.WorkflowJobStatus.IN_PROGRESS -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS
          job.status == GithubClient.WorkflowJobStatus.QUEUED -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS
          else -> MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS
        }

        val userMessage = when {
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion == "success" -> "Job '${job.name}' completed successfully"
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion == "failure" -> "Job '${job.name}' failed"
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion == "cancelled" -> "Job '${job.name}' was cancelled"
          job.status == GithubClient.WorkflowJobStatus.COMPLETED && job.conclusion == "skipped" -> "Job '${job.name}' was skipped"
          job.status == GithubClient.WorkflowJobStatus.IN_PROGRESS -> "Job '${job.name}' is running"
          job.status == GithubClient.WorkflowJobStatus.QUEUED -> "Job '${job.name}' is queued"
          else -> "Job '${job.name}' status: ${job.status.value}"
        }

        val systemMessage = buildString {
          append("Job ID: ${job.id}, Status: ${job.status.value}")
          if (job.conclusion != null) {
            append(", Conclusion: ${job.conclusion}")
          }
          if (job.startedAt != null) {
            append(", Started: ${job.startedAt}")
          }
          if (job.completedAt != null) {
            append(", Completed: ${job.completedAt}")
          }
          append(", View job: ${job.htmlUrl}")
        }

        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = "gh-workflow-job-${job.id}",
          displayName = "GitHub Job: ${job.name}",
          status = status,
          userMessage = userMessage,
          systemMessage = systemMessage,
        )
      }

      // Only include the main trigger step if no jobs have been reported yet
      val allSteps = if (seenJobIds.size == newOrUpdatedJobs.size) {
        // First time seeing jobs, include the trigger step
        listOf(
          MeshBuildingBlockRun.SourceUpdate.StepUpdate(
            id = STEP_ID,
            status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
            userMessage = "GitHub workflow triggered successfully",
            systemMessage = "Workflow started, monitoring individual jobs",
          ),
        ) + stepUpdates
      } else {
        // Just update the jobs
        stepUpdates
      }

      val update = MeshBuildingBlockRun.SourceUpdate(
        status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
        steps = allSteps,
      )

      log.debug { "Updating job statuses for run $blockRunUuid: ${newOrUpdatedJobs.map { "${it.name}(${it.status})" }}" }
      blockRunClient.updateBlockRun(update)
    }
  }

  private fun updateFinalBlockStatusFromWorkflow(
    blockRunClient: BlockRunClient,
    blockRunUuid: String,
    workflowRun: GithubClient.WorkflowRun,
  ) {
    val status = when (workflowRun.conclusion) {
      "success" -> MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED
      "failure", "cancelled", "timed_out" -> MeshBuildingBlockRun.ExecutionStatus.FAILED
      else -> MeshBuildingBlockRun.ExecutionStatus.FAILED
    }

    val userMessage = when (workflowRun.conclusion) {
      "success" -> "GitHub workflow completed successfully"
      "failure" -> "GitHub workflow failed"
      "cancelled" -> "GitHub workflow was cancelled"
      "timed_out" -> "GitHub workflow timed out"
      else -> "GitHub workflow completed with unknown status"
    }

    // Update only the trigger step for final status
    // Individual job steps will have been updated during polling
    val update = MeshBuildingBlockRun.SourceUpdate(
      status = status,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = status,
          userMessage = userMessage,
          systemMessage = "Workflow run ${workflowRun.id} completed with status: ${workflowRun.status}, conclusion: ${workflowRun.conclusion}. View run: ${workflowRun.htmlUrl}",
        ),
      ),
    )

    log.info { "GitHub workflow ${workflowRun.id} completed with conclusion '${workflowRun.conclusion}' for run: $blockRunUuid" }
    blockRunClient.updateBlockRun(update)
  }

  private fun updateFailedBlockStatusWithMeshException(blockRunClient: BlockRunClient, ex: MeshHttpException) {
    log.error(ex) { "Error contacting GitHub" }

    updateFailedBlockStatusWithMessage(
      blockRunClient,
      "Request: ${ex.requestUrl}\nGitHub responded with status: ${ex.statusCode} and body: ${ex.getResponseBody()}",
    )
  }

  private fun updateFailedBlockStatusWithException(blockRunClient: BlockRunClient, ex: Throwable) {
    log.error(ex) { "Internal error when contacting GitHub" }

    updateFailedBlockStatusWithMessage(
      blockRunClient,
      "There was an internal error while trying to contact GitHub: ${ex.message}",
    )
  }

  private fun updateFailedBlockStatusWithTimeout(blockRunClient: BlockRunClient) {
    val message = "Workflow polling timeout after $MAX_POLLING_MINUTES_WORKFLOWS minutes"

    log.error { "Timeout when contacting GitHub: $message" }

    updateFailedBlockStatusWithMessage(
      blockRunClient,
      "There was an internal error while trying to contact GitHub: $message",
    )
  }

  private fun updateFailedBlockStatusWithMessage(blockRunClient: BlockRunClient, message: String) {
    val blockRunUuid = blockRunClient.activeBlockRun.meshObject.metadata.uuid
    log.error { "Error in block run $blockRunUuid: $message" }

    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.FAILED,
          userMessage = "Could not trigger the GitHub Action",
          systemMessage = message,
        ),
      ),
    )

    blockRunClient.updateBlockRun(update)
  }


  private fun unsupportedInputSystemMessage(
    workflowName: String,
    unsupportedInput: String,
    omitRunObjectInput: Boolean,
  ): String {
    return when {
      unsupportedInput == "buildingBlockRunUrl" && omitRunObjectInput -> {
        "Your GitHub workflow '$workflowName' does not support the 'buildingBlockRunUrl' input parameter " +
          "but the 'Pass only API URL' option is enabled for this building block definition. " +
          "Please upgrade your workflow to support this input parameter and fetch building block run data from the URL. " +
          "Note: Only the URL is passed, not the full run object. " +
          "See https://github.com/meshcloud/actions-register-source/releases/tag/v2.0.0 for more details."
      }

      unsupportedInput == "buildingBlockRun" && !omitRunObjectInput -> {
        "Your GitHub workflow '$workflowName' does not support the 'buildingBlockRun' input parameter. " +
          "Please enable the 'Pass only API URL' option in your building block definition to use the modern URL-based approach instead. " +
          "Note: Only the run object is currently passed, not the URL."
      }

      unsupportedInput == BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY -> {
        "Your GitHub workflow '$workflowName' does not support the '${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}' input parameter. " +
          "This input provides an ephemeral API token for accessing the meshStack API. " +
          "Please add this input to your workflow's workflow_dispatch trigger to receive the token, for example:\n" +
          "  on:\n" +
          "    workflow_dispatch:\n" +
          "      inputs:\n" +
          "        ${BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY}:\n" +
          "          description: 'meshStack API token'\n" +
          "          required: false\n" +
          "          type: string"
      }

      unsupportedInput == BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY -> {
        "Your GitHub workflow '$workflowName' does not support the '${BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY}' input parameter. " +
          "This input provides an authentication token for updating the building block run status. " +
          "Please add this input to your workflow's workflow_dispatch trigger to receive the token, for example:\n" +
          "  on:\n" +
          "    workflow_dispatch:\n" +
          "      inputs:\n" +
          "        ${BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY}:\n" +
          "          description: 'Building block run token'\n" +
          "          required: false\n" +
          "          type: string"
      }

      else -> {
        "Your GitHub workflow '$workflowName' does not support the '$unsupportedInput' input parameter. " +
          "Please update your workflow to accept this input parameter."
      }
    }
  }

  private fun updateSuccessfulTriggerStepStatus(
    blockRunClient: BlockRunClient,
    githubWorkflow: String,
    isAsync: Boolean,
  ) {
    val extraPollingInformation = if (!isAsync) {
      "Polling for completion status..."
    } else {
      "Will wait for API updates on status..."
    }

    val update = MeshBuildingBlockRun.SourceUpdate(
      status = MeshBuildingBlockRun.ExecutionStatus.IN_PROGRESS,
      steps = listOf(
        MeshBuildingBlockRun.SourceUpdate.StepUpdate(
          id = STEP_ID,
          status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
          userMessage = "Triggered GitHub Action '$githubWorkflow'. $extraPollingInformation",
          systemMessage = "Triggered action '$githubWorkflow'. $extraPollingInformation",
        ),
      ),
    )

    val runUuid = blockRunClient.activeBlockRun.meshObject.metadata.uuid
    log.info {
      "Successfully triggered GitHub action '$githubWorkflow' for run: $runUuid, starting status polling"
    }

    blockRunClient.updateBlockRun(update)
  }

  companion object {
    @JvmStatic
    private val STEP_ID = "gh-trigger"

    const val MAX_FIND_WORKFLOW_ATTEMPTS = 12 // Try to find the actual workflow run for up to 2 minutes (12*10s)
    const val MAX_POLLING_MINUTES_WORKFLOWS = 30 // Maximum time in minutes to poll for workflows
    const val POLLING_INTERVAL_SECONDS = 10 // How often to poll for workflow status updates
  }
}
