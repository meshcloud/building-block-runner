package io.meshcloud.meshobjects

/*
 * TODO When we move the kraken endpoints to meshfed API we should consider introducing individual versioned
 *  MediaTypes. We don't want to version our API as a whole and increase the overall version of the API if we do
 *  a breaking change to one meshObject. Therefore it is important to have a versioned MediaType for every single
 *  meshObject as we already have it in meshfed-api.
 *  As this is a breaking change, the best point in time to apply it is probably when we move the public endpoint to
 *  meshfed-api anyway.
 */
object MediaTypes {
  const val MEDIA_TYPE_REQUEST_YAML_V1 = "application/vnd.meshcloud.api.meshobjects.v1+yaml"
  const val MEDIA_TYPE_REQUEST_JSON_V1 = "application/vnd.meshcloud.api.meshobjects.v1+json"
  const val MEDIA_TYPE_RESPONSE_V1 = "application/vnd.meshcloud.api.meshobjects.v1+json"
}
