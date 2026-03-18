-- Migration: Node Restarts Tracking
-- Description: Add table to track node restart events with timestamp, reason, and exit code
--              Also fix uniq_sws primary key constraint to handle nullable snd_work_id
-- Date: 2025-10-24

-- Fix uniq_sws table: snd_work_id is nullable but was part of PRIMARY KEY
-- PostgreSQL doesn't allow NULL in primary keys, so we use COALESCE with a sentinel value
ALTER TABLE uniq_sws DROP CONSTRAINT IF EXISTS uniq_sws_pkey;

-- Explicitly ensure snd_work_id allows NULL (PRIMARY KEY constraint made it NOT NULL)
ALTER TABLE uniq_sws ALTER COLUMN snd_work_id DROP NOT NULL;

-- Create unique index using COALESCE to handle NULL snd_work_id (-1 as sentinel value)
CREATE UNIQUE INDEX IF NOT EXISTS uniq_sws_unique_key 
ON uniq_sws (deployment_id, fst_work_id, COALESCE(snd_work_id, -1));

-- Update the trigger function to use the new conflict resolution based on the unique columns
CREATE OR REPLACE FUNCTION uniq_sws_trigger() RETURNS trigger AS $$ 
BEGIN
  INSERT INTO uniq_sws AS us (
    deployment_id,
    fst_work_id,
    snd_work_id,
    fee,
    prover,
    sender,
    fst_work_type,
    snd_work_type,
    fst_work_tx_hash,
    snd_work_tx_hash,
    fst_work_tx_memo,
    snd_work_tx_memo,
    time_added,
    time_received,
    time_scheduled
  ) VALUES (
    get_max_deployment_id(),
    new.fst_work_id,
    new.snd_work_id,
    new.fee,
    new.prover,
    new.sender,
    new.fst_work_type,
    new.snd_work_type,
    new.fst_work_tx_hash,
    new.snd_work_tx_hash,
    new.fst_work_tx_memo,
    new.snd_work_tx_memo,
    CASE WHEN new.action = 'added' THEN new.time ELSE NULL END,
    CASE WHEN new.action = 'received' THEN new.time ELSE NULL END,
    CASE WHEN new.action = 'scheduled' THEN new.time ELSE NULL END
  ) ON CONFLICT (deployment_id, fst_work_id, COALESCE(snd_work_id, -1)) DO UPDATE SET
    fee = COALESCE(us.fee, excluded.fee),
    prover = COALESCE(us.prover, excluded.prover),
    sender = COALESCE(us.sender, excluded.sender),
    fst_work_type = COALESCE(us.fst_work_type, excluded.fst_work_type),
    snd_work_type = COALESCE(us.snd_work_type, excluded.snd_work_type),
    fst_work_tx_hash = COALESCE(us.fst_work_tx_hash, excluded.fst_work_tx_hash),
    snd_work_tx_hash = COALESCE(us.snd_work_tx_hash, excluded.snd_work_tx_hash),
    fst_work_tx_memo = COALESCE(us.fst_work_tx_memo, excluded.fst_work_tx_memo),
    snd_work_tx_memo = COALESCE(us.snd_work_tx_memo, excluded.snd_work_tx_memo),
    time_added = COALESCE(LEAST(us.time_added, excluded.time_added), us.time_added, excluded.time_added),
    time_received = COALESCE(LEAST(us.time_received, excluded.time_received), us.time_received, excluded.time_received),
    time_scheduled = COALESCE(LEAST(us.time_scheduled, excluded.time_scheduled), us.time_scheduled, excluded.time_scheduled);
  RETURN new;
END;
$$ LANGUAGE 'plpgsql';

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
