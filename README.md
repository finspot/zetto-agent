Zetto Agent

Mainb entrypoint of the runner : polls for jobs and handle their execution

## Configuration

- ZETTO_HOST
- ZETTO_API_KEY
- ZETTO_RUNNER (e.g /usr/bin/node path/to/node/index)
- ZETTO_POLLING_INTERVAL (in seconds, default to 10)

## Runner configuration

Will be called via a shell command : $ZETTO_RUNNER <command> <input>, and will fetch output on STDOUT and logs on STDERR

Needs to respond to a global call "$ZETTO_RUNNER list", which should return a list of commands in a JSON-stringified array it can handle. May also do its boot checks, since if it does not respond successfullly, the worker will be considered down

## Installation

TODO, but ideally a curl in the image
