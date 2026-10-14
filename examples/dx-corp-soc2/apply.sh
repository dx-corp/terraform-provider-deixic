#!/usr/bin/env bash
# One-command dx-corp setup: builds the provider, points Terraform at it with a
# temporary dev_overrides CLI config, and runs terraform apply.
#
#   DEIXIC_TOKEN=<fresh bearer token> ./apply.sh [terraform apply args]
#
# DEIXIC_ENDPOINT overrides the default https://app.deixic.com. The token is read
# from the environment only and is never written to a file.
set -euo pipefail

if [ -z "${DEIXIC_TOKEN:-}" ]; then
  echo "apply.sh: DEIXIC_TOKEN is not set. Export a fresh bearer token and run again." >&2
  exit 2
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
provider_dir=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$provider_dir" && mise exec go@1.25 -- go build -buildvcs=false -o "$work/terraform-provider-deixic" .)

cat >"$work/terraformrc" <<RC
provider_installation {
  dev_overrides {
    "dx-corp/deixic" = "$work"
  }
  direct {}
}
RC

export TF_CLI_CONFIG_FILE="$work/terraformrc"
export DEIXIC_ENDPOINT="${DEIXIC_ENDPOINT:-https://app.deixic.com}"
export TF_VAR_endpoint="$DEIXIC_ENDPOINT"
export TF_DATA_DIR="$work/.terraform"

cd "$here"
if [ "${APPLY_VALIDATE_ONLY:-}" = "1" ]; then
  terraform validate
else
  terraform apply "$@"
fi
