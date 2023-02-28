# Stress Testing

This is a manually run test suite that stress tests the agent and
controller components.  Various tests (test-*.sh) will do different
setups and different stress testing.  Generally, the configs are
the same, only the services used change.

## Ports Used

The default config uses ports 8001, 8002, 8003, and 8100.

## Running Things

## One-time setup

1. Run `make` at the top level of this project.
1. `sh setup.sh` to create keys.
1. `sh run-controller.sh` to start the controller.  This is needed to generate some test keys.
1. `sh setup-service-urls.sh` to generate service keys, which will be used in the `test-*.sh` scripts.
1. Press control-c in the controller window, or leave it running for later.  It's not required to restart the controller here if you're about to use it in tests.

### Running tests

1. `sh run-controller.sh` to run the controller, if not already running.
1. `sh run-agent.sh` to run the agent.
1. `sh run-traffic-server` to start the traffic generator.
1. Run one of the `test-*.sh` scripts.

Run each of the `run-` shell commands in a new window, so you can run all three at once.

### Cleaning up

The `cleanup.sh` script will remove all certificates generated.  Once this runs, you will need to perform the one--time setup again to run tests.

## Poking around

To do some manual testing, there are some handy `curl` commands that may be interesting.

You may want to have `jq` installed to make the JSON more readable.

### Get agent statistics

```curl ...`
