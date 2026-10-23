token_file=$(readlink .meshstack/wif-token)
if ! grep -Eq "^meshstack_keycloak_wif_token_file *= *\"$token_file\"$" aaaaaa_*.auto.tfvars; then
  echo "the tf var meshstack_keycloak_wif_token_file does not name $token_file" >&2
  exit 1
fi
echo "The pre-run script found the token that the tf var names." >> "$MESHSTACK_USER_MESSAGE"
