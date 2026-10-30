expect() {
  if [ "${!1}" != "$2" ]; then
    echo "environment input $1 is '${!1}', expected '$2'" >&2
    exit 1
  fi
}
expect ENV_TEXT "hello env"
expect ENV_NUM 42
expect ENV_FLAG true
expect ENV_MULTI_SELECT '["a","c"]'
expect ENV_CODE '{"key":"value"}'
expect ENV_SECRET_SHA256 "$(printf %s "$ENV_SECRET" | sha256sum | cut -d ' ' -f 1)"
echo "The pre-run script saw every environment input." >> "$MESHSTACK_USER_MESSAGE"
