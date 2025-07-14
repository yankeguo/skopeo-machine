#!/bin/bash

cd "$(dirname "$0")"

rm -rf api/skopeo_machine/v1

export GO_POST_PROCESS_FILE="go fmt"

exec openapi-generator generate -g go-gin-server -i openapi/index.yml --enable-post-process-file --additional-properties interfaceOnly=true,apiPath=api/skopeo_machine/v1,packageName=v1