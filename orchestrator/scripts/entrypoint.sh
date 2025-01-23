#!/usr/bin/env bash
# set -x
set -e

SCRIPT_FILE="/experiment.script"

# Check for required environment variables and attached volumes
if [[ ! -f "/orchestrator-config.json" ]]; then
  echo "Error: Missing required /orchestrator-config.json file."
  exit 1
fi

if [[ ! -d "/private-keys" ]]; then
  echo "Error: Missing required /private-keys folder."
  exit 1
fi

if [[ -z "$(ls -A /private-keys)" ]]; then
  echo "Error: /private-keys folder is empty. At least one private key file is required."
  exit 1
fi

# Handle experiment script generation or usage
if [[ -f "$SCRIPT_FILE" ]]; then
  echo "Using provided $SCRIPT_FILE file for the orchestrator."
elif [[ -n "$GENERATOR_CLI_ARGS" ]]; then
  echo "Generating experiment script using CLI arguments: $GENERATOR_CLI_ARGS"
  /generator $GENERATOR_CLI_ARGS >$SCRIPT_FILE
else
  echo "Error: Either $SCRIPT_FILE file or GENERATOR_CLI_ARGS environment variable must be provided."
  exit 1
fi

# Ensure the script file exists
if [[ ! -f "$SCRIPT_FILE" ]]; then
  echo "Error: Failed to generate or locate the experiment script at $SCRIPT_FILE."
  exit 1
fi

# Launch the orchestrator with the configuration and experiment script
echo "Starting orchestrator with configuration file and experiment script."
exec /orchestrator /orchestrator-config.json <"$SCRIPT_FILE"
