package io.meshcloud.buildingblocks.runner.runclient

class MeshStackRejectedRequestException(
  val statusCode: Int,
  message: String,
) : Exception(message)

/**
 * The runner cannot tell whether meshStack took a request of a run: meshStack stayed unavailable past the retries,
 * or rejected a retry after an earlier attempt that may have arrived.
 * For the final status it ends the run: reporting it to meshStack would wait just as long again, or could overwrite
 * a status that did arrive.
 */
class MeshStackRequestOutcomeUnknownException(
  message: String,
  cause: Throwable,
) : Exception(message, cause)
