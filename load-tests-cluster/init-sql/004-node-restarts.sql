-- Migration: Node Restarts Tracking
-- Description: Add table to track node restart events with timestamp, reason, and exit code
--              Also fix uniq_sws primary key constraint to handle nullable snd_work_id
-- Date: 2025-10-24

-- Fix uniq_sws table: snd_work_id is nullable but was part of PRIMARY KEY
-- PostgreSQL doesn't allow NULL in primary keys, so we use COALESCE with a sentinel value
ALTER TABLE uniq_sws DROP CONSTRAINT IF EXISTS uniq_sws_pkey;

-- Create unique index using COALESCE to handle NULL snd_work_id (-1 as sentinel value)
CREATE UNIQUE INDEX IF NOT EXISTS uniq_sws_unique_key 
ON uniq_sws (deployment_id, fst_work_id, COALESCE(snd_work_id, -1));

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
