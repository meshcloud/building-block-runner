package io.meshcloud.exception

open class MeshException(
  val userMessage: String,
  systemMessage: String,
  cause: Throwable? = null
) : MeshSystemException(systemMessage, cause) {

  /**
   * A user-facing, semantic code for the type of error.
   *
   * This is useful so that API clients can discern different types of errors
   * - without parsing error _messages_, which are intended for human consumption.
   * - when different types of errors need to share the same HTTP status code, e.g. 400 Bad Request
   *
   * Exposing the error code is fine for MeshException because we explicitly define them to be meaningful
   * in our domain model, instead of java built-in/library exceptions which are not.
   *
   * Many public APIs expose error codes and document them, e.g.
   * https://docs.aws.amazon.com/AWSEC2/latest/APIReference/errors-overview.html
   *
   * Note:
   * We use prefixed names internally a lot like "MeshBadRequestException" and thus trim the
   * suffix/prefix so that we get a more readable error code like "BadRequest"
   *
   */
  val errorCode: String = this.javaClass.simpleName
    .removePrefix("Mesh")
    .removeSuffix("Exception")

  constructor(
    userMessage: String,
    cause: Throwable? = null
  ) : this(systemMessage = userMessage, userMessage = userMessage, cause = cause)

  /**
   * This builds a new exception but with a new user message. This is helpful
   * if you want to rewrite the user facing message on a "higher layer" where more
   * information is available then where the message is thrown.
   */
  fun copyWithUserMessage(userMessage: String): MeshException {
    return MeshException(
      userMessage = userMessage,
      systemMessage = systemMessage,
      cause = cause
    )
  }
}
