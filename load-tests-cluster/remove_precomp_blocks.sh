#!/bin/bash

set -x

set -e pipefail


if gcloud storage ls 'gs://testnet-precomputed-blocks/**'; then 
	gcloud storage rm 'gs://testnet-precomputed-blocks/**' || true
fi
