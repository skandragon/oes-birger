#!/bin/sh

set -e

# Generate a URL with a key for each service.  These will be generated with name and type to
# be the same as the service name.
services="traffic"

for service in $services; do
    [ -r ${service}.url ] && {
        echo "${service}.url exists.  Run sh clean.sh to reset before running this script."
        exit 1
    }
    ../bin/forwarder-get-creds \
        --action service --agent smith \
        --name ${service} \
        --type ${service} \
        --url https://localhost:8002 \
        --output-file ${service}.url
done
