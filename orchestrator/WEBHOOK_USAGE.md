# Webhook Integration for Orchestrator Service

The orchestrator service now supports webhook notifications to notify external systems when experiments complete or fail.

## How to Use

### 1. Include Webhook URL in Experiment Request

When creating an experiment via the `/api/v0/experiment/run` endpoint, include an optional `webhook_url` parameter:

```json
{
  "experiment_name": "my-test-experiment",
  "webhook_url": "https://your-server.com/webhook/endpoint",
  "base_tps": 10.0,
  "rounds": 3
}
```

(plus any other experiment parameters; every field except `experiment_name`
falls back to its default.)

### Allowed webhook destinations

`webhook_url` is supplied by the caller and the endpoint is unauthenticated, so
the orchestrator refuses destinations that would turn it into an arbitrary-POST
primitive inside the cluster:

| destination | accepted |
|---|---|
| `https://hooks.slack.com/services/...` | yes |
| `http://example.com/hook` | yes |
| `http://10.0.0.5:9200/...` (RFC1918 / RFC4193) | only with `-allow-private-webhooks` |
| `http://127.0.0.1:...`, `http://localhost:...` | never |
| `http://169.254.169.254/...` (cloud metadata) | never |
| `http://100.64.0.1/...` and other reserved ranges | never |
| any scheme other than `http`/`https` | never |

A host name is checked against every address it resolves to, the address the
socket connects to is checked a second time at dial time (so a DNS record that
answers differently for the two lookups gains nothing), and redirects are not
followed, so a public destination cannot bounce the POST to an internal one.
The destination is checked when the experiment is created, so an unusable
`webhook_url` is a 400 rather than a silent failure hours later.

The URL is treated as a credential: it is not carried by the `GET
/api/v0/experiment/status` payload -- neither as a field nor inside the
generated script comment the logs hold -- and only its scheme and host are
logged.

### When a notification is sent

One notification is sent per experiment, when the orchestrator has finished
running it. "Finished" means the generated script has run to its end, and the
last round waits out its own load, so a success notification means the load has
been delivered, not only scheduled.

An experiment stopped with `POST /api/v0/experiment/cancel` is not a failure:
its terminal status is `cancelled` and **no** webhook is sent, of either kind.

### 2. Webhook Payload Format

When the experiment completes (either successfully or with an error), the orchestrator will send a POST request to your webhook URL with the following JSON payload:

#### Success Notification
```json
{
  "experiment_name": "my-test-experiment",
  "success": true,
  "warnings": ["Optional warning messages"]
}
```

#### Error Notification
```json
{
  "experiment_name": "my-test-experiment", 
  "success": false,
  "error": "Detailed error message",
  "warnings": ["Optional warning messages"]
}
```

### 3. Webhook Request Headers

The webhook request will include the following headers:
- `Content-Type: application/json`
- `User-Agent: mina-orchestrator/1.0`

### 4. Webhook Endpoint Requirements

Your webhook endpoint should:
- Accept POST requests
- Return HTTP status code 200-299 for successful processing
- Process the request within 30 seconds (request timeout)

## Error Handling

- If the webhook URL is unreachable or returns an error status code, the notification will fail but the experiment will continue normally
- Webhook notifications are sent asynchronously and do not affect experiment execution
- Failed webhook notifications are logged but do not cause the experiment to fail
- The webhook request has a 30-second timeout

## Troubleshooting

- Check orchestrator service logs for webhook-related errors
- Verify your webhook endpoint is accessible from the orchestrator service
- Ensure your webhook endpoint returns appropriate HTTP status codes
- Test with a simple webhook testing service first before implementing custom logic
