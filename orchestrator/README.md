# ITN Orchestrator

ITN Orchestrator is a tool that manages the experiment.

Orchestrator takes a single CLI argument, path to JSON config.

It reads steps to run from Stdin. Each step is encoded in JSON. It outputs logs to Stderr
(and possibly to file, if `logFile` parameter is set in config). It outputs results of performing
steps in JSON format to Stdout.

Example of execution for ITN Orchestrator is as follows:

```sh
cat test.script | GOOGLE_APPLICATION_CREDENTIALS=credentials.json ./orchestrator config.json | tee test1.out
```

Environment variable `GOOGLE_APPLICATION_CREDENTIALS` is provided to allow the ITN Orchestrator
to communicate with Google Storage Bucket configured by `uptimeBucket` parameter of config.

Example of `config.json` (parameters `logFile` and `logLevel` are optional):

```json
{
  "key": "2Dtcua6w9g8JZczc/D6laz6Yn1ZP7DVGCmHfFDxGupY=",
  "uptimeBucket": "georgeee-uptime-itn-test-2",
  "logLevel": "info",
  "logFile": "a.log"
}
```

Example of `test.script` is below:

```json
{"action":"load-keys","params":{"dir":"./keys","limit":6}}
{"action":"discovery","params":{"offsetMin":15,"limit":2}}
{"action":"payments","params":
  {"experimentName":    "test-4",
   "tps":               0.05,
   "durationInMinutes": 10,
   "feeMax":            1e9,
   "feeMin":            2e9,
   "amount":            1e8,
   "receiver":          "B62qpPita1s7Dbnr7MVb3UK8fdssZixL1a4536aeMYxbTJEtRGGyS8U",
   "senders":           {"type":"output", "step": -2, "name":"key"},
   "nodes":             {"type":"output", "step": -1, "name":"participant"}
  }}
{"action":"wait","params":{"minutes":3}}
{"action":"stop","params":{"receipts":
  {"type":"output", "step": -2, "name":"receipt"}
}}
```

This script performs the following steps:

1. Loads 6 keys from `./keys` directory (in Mina keyfile format with empty passwords)
2. Finds 2 nodes that are online and authorize the connection from Orchestrator
3. Schedules payments through discovered nodes and loaded secret keys (each node is provided 3 secret keys and sends 3 transactions per minute for 10 minites)
4. Waits for 3 minutes
5. Stops payments sent on step 3

Parameter of a step is defined either as a JSON value or a reference to an output of previous step, like:

```json
{ "type": "output", "step": -1, "name": "participant" }
```

(example makes reference to a value produced by previous step with name `participant`).

Each step may have many outputs. Single outputs are formatted in Stdout as:

```json
{ "step": 5, "name": "example", "value": "<some value>" }
```

List outputs are formated in Stdout as a number of entries (with same step and name):

```json
{
  "step": 0,
  "name": "key",
  "multi": true,
  "value": "EKDhaEurqVTbuGRqrVe2SYZwrsnaQewLCQQS5PitEAdXxcG6vB2i"
}
```

Step parameter is either identifier of step (steps are counted from `0`) or a negative number `-x` referring to an execution that was `x` steps before. E.g. `step: -1` refers to output of the previous step.

## Import of output from another run

It's possible to use outputs of one run in another run. E.g. the following two execution may achieve this goal:

```sh
cat start.script | GOOGLE_APPLICATION_CREDENTIALS=credentials.json ./orchestrator config.json | tee start.out

cat stop.script | GOOGLE_APPLICATION_CREDENTIALS=credentials.json ./orchestrator config.json | tee stop.out
```

Where `start.script` is defined as:

```json
{"action":"load-keys","params":{"dir":"./keys","limit":6}}
{"action":"discovery","params":{"offsetMin":15,"limit":2}}
{"action":"payments","params":{
    "experimentName":    "test-4",
    "tps":               0.05,
    "durationInMinutes": 10,
    "feeMax":            1000000000,
    "feeMin":            2000000000,
    "amount":            100000000,
    "receiver":          "B62qpPita1s7Dbnr7MVb3UK8fdssZixL1a4536aeMYxbTJEtRGGyS8U",
    "senders":           {"type":"output", "step": -2, "name":"key"},
    "nodes":             {"type":"output", "step": -1, "name":"participant"}
}}
```

And `stop.script` is defined as:

```json
{
  "action": "stop",
  "params": {
    "receipts": {
      "type": "output",
      "step": 2,
      "name": "receipt",
      "file": "start.out"
    }
  }
}
```

## Nuances of load-keys

Unlike other steps, load-keys outputs are not dumped to Stdout.
That means that it isn't possible to reuse these outputs from other runs: keys have to be loaded again.

Keyloader takes a number of parameters:

```json
{
  "action": "load-keys",
  "params": { "dir": "./keys2", "password-env": "PASS", "limit": 4 }
}
```

Parameters `limit` and `password-env` are optional. When a non-zero `limit` is provided, at most `limit`
keys will be loaded from the directory. When no `password-env` is provided, password is assumed to be empty.
When `password-env` is provided, an environment variable with the name specified will be checked for the password.

E.g. with parameters above and environment variable `PASS=abra` password `abra` will be used to try to
load and unseal key.

Files with `.pub` extensions are ignored. All other files are treated as secret keys. If opening or unsealing the key
fails, the step fails.

## Funding keys

To generate many keys from a single originating key, use the following action:

```json
{
  "action": "fund-keys",
  "params": {
    "amount": 200000000000,
    "fee": 1000000000,
    "prefix": "./keys/key",
    "num": 6,
    "privkey": "./root-key",
    "password-env": "PASS"
  }
}
```

When no `password-env` is provided, empty password will be used to decode the originating private key (`./root-key`)
and to encode new private keys (`./keys/key-0`, `./keys/key-1` ...).

## Debug Printout

You can enable debug printout of graphql requests by adding 

`"printRequests":"true"` 

in config.json **and** set `"logLevel":"debug"` or lower

## Build from sources

- Make sure you have `Go v1.20+` installed.
  - One can use [Go version manager](https://github.com/moovweb/gvm), for example.
- Use `Makefile` targets to build executables:
  - `make generator`
  - `make orchestrator`

# Orchestrator as Service

The Orchestrator can be run as a service, exposing a set of HTTP endpoints to manage experiments. Below are the available endpoints:

### 1. Run Experiment
Starts a new experiment with the provided setup.

```bash
curl --location 'http://{host}:9090/api/v0/experiment/run' \
--header 'Content-Type: application/json' \
--data '{
  "experiment_setup": {
        "priv_keys":["/keys/plain1"],
        "payment_receiver": "B62qnKweK4BVxG7TA1VzhNr6GcTejXbrN6ycEQiW4ZgUCxHuWTQta4i",
        "experiment_name":"new_experiment",
        "zkapp_ratio": 0.3,
        "stress_tps": 0.5
  }  
}'
```

Where experiment_setup is complete generator config set.

### 2. Check Experiment Status
Retrieves the current status of the running experiment.

```bash
curl --location 'http://host:9090/api/v0/experiment/status'
```

### 3. Cancel Experiment
Stops the currently running experiment.

```bash
curl --location --request POST 'http://hosts:9090/api/v0/experiment/cancel'
```

### 4. Test Experiment Setup
Tests the experiment setup without actually running it.

```bash
curl --location 'http://localhost:9090/api/v0/experiment/test' \
--header 'Content-Type: application/json' \
--data '{
  "experiment_setup": {
        "priv_keys":["plain1"],
        "payment_receiver": "B62qnKweK4BVxG7TA1VzhNr6GcTejXbrN6ycEQiW4ZgUCxHuWTQta4i",
        "experiment_name":"exp",
        "zkapp_ratio": 0.3,
        "stress_tps": 0.5
  }  
}'
```

### Notes
- Ensure the Orchestrator service is running and accessible at the specified host and port.
- The `zkapp_ratio` and `stress_tps` parameters control the experiment's behavior and load.
- Use appropriate paths for private keys and other resources.
- The `test` endpoint is useful for validating configurations before running an actual experiment.