-- Migration: Enhanced Snark Work Monitoring
-- Description: Update block_trace_update_started_at trigger to exclude additional snark work events
-- Date: 2025-09-30

-- Drop existing trigger
DROP TRIGGER IF EXISTS block_trace_update_started_at ON block_trace_checkpoint;

-- Recreate trigger with updated event exclusions
CREATE TRIGGER block_trace_update_started_at AFTER INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND NOT new.is_control
    AND new.source = 'M'
    AND new.name NOT IN (
        'Snark_work_received',
        'Snark_work_rejected',
        'Snark_work_accepted',
        'Snark_work_removed',
        'Snark_work_added',
        'Snark_work_scheduled',
        'Transaction_diff_received',
        'Transaction_diff_rejected',
        'Transaction_diff_accepted'
    )
) EXECUTE FUNCTION block_trace_update_started_at_trigger();

-- Extend pool_action_t enum to include snark work specific actions
ALTER TYPE pool_action_t ADD VALUE IF NOT EXISTS 'added';
ALTER TYPE pool_action_t ADD VALUE IF NOT EXISTS 'scheduled';
ALTER TYPE pool_action_t ADD VALUE IF NOT EXISTS 'removed';

-- Update gossip_traces_trigger to handle all snark work events
CREATE OR REPLACE FUNCTION gossip_traces_trigger() RETURNS trigger AS $$ 
DECLARE 
  nresource resource_t;
  naction pool_action_t;
  nstarted_at timestamptz;
BEGIN 
  NEW.gossip := TRUE;
  nstarted_at := to_timestamp(new.started_at);

  IF new.name IN (
    'Snark_work_received',
    'Snark_work_rejected',
    'Snark_work_accepted',
    'Snark_work_added',
    'Snark_work_scheduled',
    'Snark_work_removed'
  ) THEN 
    nresource := 'Snark_work';
    naction := SUBSTRING(new.name FROM 12)::pool_action_t;
  ELSE 
    nresource := 'Transaction_diff';
    naction := SUBSTRING(new.name FROM 18)::pool_action_t;
  END IF;

  INSERT INTO last_block_trace_gossip_checkpoint (
    block_trace_id, 
    resource, 
    action, 
    started_at
  ) VALUES (
    new.block_trace_id,
    nresource,
    naction,
    nstarted_at
  ) ON CONFLICT ON CONSTRAINT last_block_trace_gossip_checkpoint_pkey DO UPDATE SET
    action = naction,
    started_at = nstarted_at;

  RETURN NEW;
END;
$$ LANGUAGE 'plpgsql';

-- Update gossip_traces_on_add trigger to handle all snark work events
DROP TRIGGER IF EXISTS gossip_traces_on_add ON block_trace_checkpoint;
CREATE TRIGGER gossip_traces_on_add BEFORE INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND NOT new.is_control
    AND new.source = 'M'
    AND new.name IN (
        'Snark_work_received',
        'Snark_work_rejected',
        'Snark_work_accepted',
        'Snark_work_added',
        'Snark_work_scheduled',
        'Snark_work_removed',
        'Transaction_diff_received',
        'Transaction_diff_rejected',
        'Transaction_diff_accepted'
    )
) EXECUTE FUNCTION gossip_traces_trigger();

-- Fix NOT NULL constraints that are too strict for optional metadata fields
ALTER TABLE sw_traces
  ALTER COLUMN fee DROP NOT NULL,
  ALTER COLUMN prover DROP NOT NULL;

-- Add transaction information columns to sw_traces table
ALTER TABLE sw_traces
  ADD COLUMN IF NOT EXISTS fst_work_type VARCHAR(64),
  ADD COLUMN IF NOT EXISTS snd_work_type VARCHAR(64),
  ADD COLUMN IF NOT EXISTS fst_work_tx_hash VARCHAR(128),
  ADD COLUMN IF NOT EXISTS snd_work_tx_hash VARCHAR(128),
  ADD COLUMN IF NOT EXISTS fst_work_tx_memo TEXT,
  ADD COLUMN IF NOT EXISTS snd_work_tx_memo TEXT;

-- Update sw_traces_add function to populate transaction information from metadata's txs field
CREATE OR REPLACE FUNCTION sw_traces_add(id bigint, block_trace_id_ int4, metadata jsonb) 
RETURNS SETOF sw_traces AS $$ 
BEGIN
  RETURN QUERY
  INSERT INTO sw_traces (
    block_trace_checkpoint_id,
    node_name,
    action,
    time,
    fst_work_id,
    snd_work_id,
    fee,
    prover,
    sender,
    reason,
    fst_work_type,
    snd_work_type,
    fst_work_tx_hash,
    snd_work_tx_hash,
    fst_work_tx_memo,
    snd_work_tx_memo
  ) (
    SELECT
      id,
      node_name,
      action,
      started_at,
      CAST(metadata #>> '{work_ids,0}' AS integer) fst_work_id,
      CAST(metadata #>> '{work_ids,1}' AS integer) snd_work_id,
      metadata ->> 'fee' fee,
      metadata ->> 'prover' prover,
      metadata ->> 'sender' sender,
      metadata ->> 'reason' reason,
      metadata #>> '{txs,0,0}' fst_work_type,
      metadata #>> '{txs,1,0}' snd_work_type,
      metadata #>> '{txs,0,1}' fst_work_tx_hash,
      metadata #>> '{txs,1,1}' snd_work_tx_hash,
      metadata #>> '{txs,0,2}' fst_work_tx_memo,
      metadata #>> '{txs,1,2}' snd_work_tx_memo
    FROM
      (SELECT b.node_name, p.*
      FROM
        last_block_trace_gossip_checkpoint p
        JOIN block_trace b ON b.block_trace_id = p.block_trace_id
    WHERE
        p.block_trace_id = block_trace_id_
        AND p.resource = 'Snark_work') AS sub
  ) RETURNING *;
END;
$$ LANGUAGE 'plpgsql';

-- Table to store unique snark work records
CREATE TABLE IF NOT EXISTS uniq_sws (
  deployment_id INT NOT NULL,
  fst_work_id INT NOT NULL,
  snd_work_id INT,
  fee VARCHAR(255),
  prover VARCHAR(255),
  sender VARCHAR(255),
  fst_work_type VARCHAR(64),
  snd_work_type VARCHAR(64),
  fst_work_tx_hash VARCHAR(128),
  snd_work_tx_hash VARCHAR(128),
  fst_work_tx_memo TEXT,
  snd_work_tx_memo TEXT,
  time_added TIMESTAMPTZ,
  time_received TIMESTAMPTZ,
  time_scheduled TIMESTAMPTZ,
  PRIMARY KEY (deployment_id, fst_work_id, snd_work_id)
);

-- Trigger function to populate uniq_sws from sw_traces
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
  ) ON CONFLICT ON CONSTRAINT uniq_sws_pkey DO UPDATE SET
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

  RETURN NULL;
END;
$$ LANGUAGE 'plpgsql';

-- Create trigger on sw_traces to populate uniq_sws
DROP TRIGGER IF EXISTS sw_traces_handle_sws ON sw_traces;
CREATE TRIGGER sw_traces_handle_sws AFTER INSERT ON sw_traces
FOR EACH ROW
EXECUTE FUNCTION uniq_sws_trigger();

-- View for snark work statistics grouped by experiment, round, and transaction type
CREATE OR REPLACE VIEW sw_experiments AS
WITH sws_ AS (
  -- Extract exp and round from transaction memos of form {name}-{round}-{batch}-{ix}
  SELECT
    regexp_replace(fst_work_tx_memo, '-\d+-\d+-\d+$', '') as fst_exp,
    CAST((regexp_matches(fst_work_tx_memo, '^(.*)-(\d+)-(\d+)-(\d+)$'))[2] AS int) as fst_round,
    regexp_replace(snd_work_tx_memo, '-\d+-\d+-\d+$', '') as snd_exp,
    CAST((regexp_matches(snd_work_tx_memo, '^(.*)-(\d+)-(\d+)-(\d+)$'))[2] AS int) as snd_round,
    fst_work_type,
    snd_work_type,
    deployment_id,
    time_added,
    time_received,
    time_scheduled
  FROM
    uniq_sws
  WHERE
    fst_work_tx_memo ~ '-\d+-\d+-\d+$'
    AND snd_work_tx_memo ~ '-\d+-\d+-\d+$'
)
SELECT
  fst_exp as exp,
  fst_round as round,
  deployment_id,
  fst_work_type as fst_tx_type,
  snd_work_type as snd_tx_type,
  COUNT(*) as total_sws,
  -- Time difference: received - scheduled
  MIN(EXTRACT(EPOCH FROM (time_received - time_scheduled))) as min_received_scheduled_sec,
  AVG(EXTRACT(EPOCH FROM (time_received - time_scheduled))) as avg_received_scheduled_sec,
  MAX(EXTRACT(EPOCH FROM (time_received - time_scheduled))) as max_received_scheduled_sec,
  -- Time difference: received - added
  MIN(EXTRACT(EPOCH FROM (time_received - time_added))) as min_received_added_sec,
  AVG(EXTRACT(EPOCH FROM (time_received - time_added))) as avg_received_added_sec,
  MAX(EXTRACT(EPOCH FROM (time_received - time_added))) as max_received_added_sec
FROM
  sws_
WHERE
  -- Only include entries where both transactions have the same exp and round
  fst_exp = snd_exp
  AND fst_round = snd_round
GROUP BY
  exp, round, deployment_id, fst_tx_type, snd_tx_type
ORDER BY
  deployment_id, exp, round, fst_tx_type, snd_tx_type;

  