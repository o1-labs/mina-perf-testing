# Load Tests Cluster Infrastructure

This directory contains the Docker Compose infrastructure for running performance testing and monitoring of the Mina network. It provides a complete environment with log fetching, data persistence, dashboard visualization, and experiment orchestration.

## Architecture Overview

The system consists of several interconnected services that work together to provide comprehensive performance testing and monitoring capabilities:

### Core Services

- **postgres**: PostgreSQL database for storing experiment data, logs, and metrics
- **log-fetcher**: Fetches internal trace logs from Mina nodes and stores them in the database
- **log-api**: API service for querying stored log data
- **experiments-api**: Backend API for managing experiment data and metadata
- **dashboard**: Web-based visualization dashboard for monitoring and analysis
- **in-memory-uptime-backend**: Tracks active Mina nodes and their uptime status
- **orchestrator-as-service**: Service for running and managing performance experiments

### Data Flow

The system follows this workflow pattern based on the tracing flow documentation:

1. **Environment Management**:
   - Remove old environments and freeze tracing
   - Create new environments and bump deployment IDs
   - Clean old unfinished tracing data

2. **Experiment Execution**:
   - Unfreeze tracing to begin data collection
   - Run experiments via the orchestrator
   - Monitor experiment status and progress

3. **Data Collection & Storage**:
   - Internal log fetcher requests logs from Mina nodes
   - Logs are processed and stored in PostgreSQL
   - Uptime backend tracks node availability
   - Dashboard API provides real-time data access

## Prerequisites

- Server with [Docker Compose](https://docs.docker.com/compose/) installed
- Sufficient disk space for PostgreSQL data storage
- Network access to target Mina nodes

## Quick Start

1. Clone this repository and navigate to the load-tests-cluster directory
2. Copy the environment template and configure your settings:
   ```bash
   cp .env.example .env
   # Edit .env with your specific configuration
   ```
3. Start all services:
   ```bash
   docker compose up -d --remove-orphans
   ```
4. Stop services when done:
   ```bash
   docker compose down
   ```

## Service Endpoints

- **Dashboard**: http://localhost:4200
- **Log Fetcher API**: http://localhost:4000
- **Log Query API**: http://localhost:9080
- **Experiments API**: http://localhost:3003
- **Uptime Backend**: http://localhost:8080
- **Orchestrator Service**: http://localhost:9090
- **PostgreSQL**: localhost:5432

## Configuration

### Environment Variables (.env file)

Key configuration options include:

- `NETWORK_NAME`: Docker network name (default: o1labs-fetcher)
- `POSTGRES_*`: Database connection settings
- `UPTIME_BACKEND_API_URL`: URL for the uptime tracking service
- `HOST_OVERRIDES`: Host override settings for log fetching

### Service Configurations

Individual services have their own configuration files:

- `orchestrator-as-service/config.json`: Orchestrator service settings
- `uptime-backend/config.json`: Uptime backend configuration
- `init-sql/`: Database initialization scripts

## API Usage Examples

### Tracing Control

Freeze tracing (stop data collection):
```bash
curl --location 'http://localhost:4000/freeze'
```

Unfreeze tracing (resume data collection):
```bash
curl --location 'http://localhost:4000/unfreeze'
```

### Deployment Management

Create new deployment entry:
```bash
curl --location 'http://localhost:3003/api/deployments' \
--header 'Content-Type: application/json' \
--data '{
  "data": {
    "commit": "https://github.com/o1-labs/gitops-infrastructure/commit/YOUR_COMMIT_HASH"
  }
}'
```

### Experiment Management

Run an experiment:
```bash
curl --location 'http://localhost:9090/api/v0/experiment/run' \
--header 'Content-Type: application/json' \
--data '{
  "experiment_setup": {
    "priv_keys": ["/keys/plain1"],
    "payment_receiver": "B62qnKweK4BVxG7TA1VzhNr6GcTejXbrN6ycEQiW4ZgUCxHuWTQta4i",
    "experiment_name": "test-experiment-1"
  }
}'
```

Check experiment status:
```bash
curl --location 'http://localhost:9090/api/v0/experiment/status'
```

### Example of full cycle

Let's say, that we want to redeploy cluster and start new experiment. Below, we present chain of interaction with services:

1. Stop tracing for current deployment (Freeze Tracing)
2. (Redeploy environment)
3. Bump deployment id
4. Unfreeze tracing
5. Start experiment

## Database Queries

### Useful SQL Queries

Get latest blocks with transaction type breakdown:
```sql
-- Get latest blocks with breakdown by count of each transaction type
-- Note: 2000 is the limit of lookup in block_trace table, actual number of blocks will be less
-- Blocks that have no payments and no zkapp transactions will not be shown

SELECT bt.block_id, bt.time, txs.value ->> 0 as type, count(*) cnt
FROM (
  SELECT block_id, max(meta::text)::jsonb meta, to_timestamp(max(time)) time
  FROM (
    SELECT block_id, metadata_json meta, trace_started_at time
    FROM block_trace
    ORDER BY block_trace_id DESC
    LIMIT 2000
  ) AS subquery1
  GROUP BY block_id
) bt
CROSS JOIN jsonb_array_elements(bt.meta -> 'transactions') txs
WHERE txs.value ->> 0 NOT IN ('coinbase', 'fee_transfer')
GROUP BY bt.block_id, bt.time, txs.value ->> 0
ORDER BY bt.time DESC;
```

## Troubleshooting

- Check service logs: `docker compose logs [service-name]`
- Verify all services are running: `docker compose ps`
- Ensure proper file permissions for mounted volumes
- Check network connectivity between services and external Mina nodes

## Development

For development and debugging:

1. Use `docker compose logs -f` to follow logs in real-time
2. Access individual service containers with `docker compose exec [service] bash`
3. Database can be accessed directly via PostgreSQL client on port 5432
4. Configuration changes require service restart: `docker compose restart [service]`
