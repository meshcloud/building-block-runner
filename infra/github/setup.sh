# shellcheck shell=bash # note: this script must also work when sourced from zsh
# Source this file to set up environment variables for infra/github locally.
# Usage: source setup.sh

# Needs:
# * Hashicorp Vault CLI (command vault)
# * kubectl with access to the meshcloud internal infra kubernetes cluster hosting Vault

_setup_vault_portforward() {
	local kube_context
	kube_context=$(kubectl config get-contexts -o name | grep '^gke_meshstack-infra.*_meshstack-infra$' | head -n1)
	if [[ -z "$kube_context" ]]; then
		echo "Failed to find kubectl context with prefix gke_meshstack-infra. Try:" >&2
		echo "gcloud container clusters get-credentials meshstack-infra --region europe-west3 --project meshstack-infra" >&2
		return 1
	fi

	local kubectl_output
	kubectl_output=$(mktemp)

	# Suppress job control messages in interactive shells (zsh)
	[[ -o monitor ]] && local _restore_monitor=1 && set +m
	kubectl port-forward --context "$kube_context" -n vault-new svc/vault :8200 >"$kubectl_output" 2>&1 &
	_VAULT_PF_PID=$!
	[[ -n "${_restore_monitor:-}" ]] && set -m

	local local_vault_port
	for i in $(seq 10 -1 0); do
		local_vault_port=$(sed -n 's/.*Forwarding from 127.0.0.1:\([0-9]*\).*/\1/p' "$kubectl_output" 2>/dev/null || true)
		if [[ -n "$local_vault_port" ]]; then
			export VAULT_ADDR="http://localhost:$local_vault_port"
			echo "Opened port-forward to Vault at $VAULT_ADDR" >&2
			break
		elif (( i == 0 )); then
			echo "Failed to establish Vault port-forward:" >&2
			cat "$kubectl_output" >&2
			kill "$_VAULT_PF_PID" 2>/dev/null || true
			rm -f "$kubectl_output"
			return 1
		fi
		sleep 0.5
	done

	rm -f "$kubectl_output"

	if ! vault token lookup &>/dev/null; then
		vault login -method=oidc >&2 || {
			kill "$_VAULT_PF_PID" 2>/dev/null || true
			return 1
		}
	fi
}

_run_setup() {
	echo "setup.sh: Exporting environment variables:" >&2

	local slack_webhook_url
	slack_webhook_url=$(vault kv get -field="SLACK_WEBHOOK_URL" concourse/meshstack-dev/building-block-runner) || return 1

	export SLACK_WEBHOOK_URL="$slack_webhook_url"
	echo "  SLACK_WEBHOOK_URL" >&2

	export TF_VAR_slack_webhook_url="$slack_webhook_url"
	echo "  TF_VAR_slack_webhook_url" >&2
}

_setup_vault_portforward || return 1
_run_setup
_setup_rc=$?

# Always stop the port-forward and clean up.
[[ -o monitor ]] && _restore_monitor=1 && set +m
kill "$_VAULT_PF_PID" 2>/dev/null && wait "$_VAULT_PF_PID" 2>/dev/null
[[ -n "${_restore_monitor:-}" ]] && set -m

unset VAULT_ADDR _VAULT_PF_PID _restore_monitor
unset -f _setup_vault_portforward _run_setup

if [[ $_setup_rc -ne 0 ]]; then
	unset _setup_rc
	echo "setup.sh: Failed." >&2
	return 1
fi

unset _setup_rc
echo "setup.sh: Done." >&2

