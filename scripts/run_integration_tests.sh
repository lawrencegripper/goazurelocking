#/bin/bash
set -e

if [ -f .env ]; then
    set -o allexport
    source .env
    set +o allexport
fi

if [ ! -z "$AZURE_STORAGE_NAME" ]; then 
    go test -v -timeout 5m ./...
else
    echo "Skipping integration tests and variables not set"
    echo "Set 'AZURE_STORAGE_NAME' and 'AZURE_STORAGE_KEY' either in environment vars or .env file at project root"
fi 