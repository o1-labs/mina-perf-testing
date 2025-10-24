-- Migration: Node Restarts Tracking
-- Description: Add table to track node restart events with timestamp, reason, and exit code
-- Date: 2025-10-24

-- Table to store node restart events
CREATE TABLE IF NOT EXISTS node_restarts (
  id SERIAL PRIMARY KEY,
  deployment_id INTEGER NOT NULL DEFAULT get_max_deployment_id(),
  node_name VARCHAR NOT NULL,
  time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  reason TEXT,
  code INTEGER,
  object_name VARCHAR,
  object_namespace VARCHAR,
  container_name VARCHAR,
  restart_count INTEGER,
  FOREIGN KEY (deployment_id) REFERENCES deployment(deployment_id)
);

-- Index for quick lookup of restarts by node name
CREATE INDEX IF NOT EXISTS node_restarts_node_name_idx ON node_restarts (node_name);

-- Index for quick lookup of restarts by time
CREATE INDEX IF NOT EXISTS node_restarts_time_idx ON node_restarts (time);
