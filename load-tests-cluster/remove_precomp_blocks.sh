#!/bin/bash

set -x

set -e pipefail


if gsutil ls 'gs://testnet-precomputed-blocks/**'; then 
	gsutil -m rm 'gs://testnet-precomputed-blocks/**' || true
fi
