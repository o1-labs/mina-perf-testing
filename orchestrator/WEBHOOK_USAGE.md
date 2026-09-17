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
  "rounds": 3,
  // ... other experiment parameters
}
```

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
