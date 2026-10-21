package executor

// InheritedEnv lists the variables of the runner's env that reach every process of a run; the rest may hold its credentials.
var InheritedEnv = []string{
	"PATH", "HOME", "TMPDIR", "TMP", "TEMP",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_DIR", "SSL_CERT_FILE", "CUSTOM_CA_CERTS_PATH",
	"SSH_KNOWN_HOSTS", "NIX_CONFIG", "MESHSTACK_SKIP_VERSION_CHECK",
	"TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH", "CHECKPOINT_DISABLE", "TF_IN_AUTOMATION",
}
