
-- Table to store deployment information, including a unique ID, start time, and metadata.
CREATE TABLE IF NOT EXISTS deployment (
  deployment_id SERIAL PRIMARY KEY,
  metadata_json JSONB NOT NULL
);

-- Table to store block trace information, including details about the block, timing, and metadata.
CREATE TABLE IF NOT EXISTS block_trace (
  block_trace_id SERIAL PRIMARY KEY,
  deployment_id INTEGER NOT NULL,
  node_name VARCHAR NOT NULL,
  block_id VARCHAR NOT NULL,
  trace_started_at FLOAT NOT NULL,
  trace_completed_at FLOAT,
  total_time FLOAT NOT NULL,
  source VARCHAR NOT NULL,
  blockchain_length INT NOT NULL,
  global_slot INT NOT NULL,
  status VARCHAR NOT NULL,
  metadata_json JSONB NOT NULL,
  FOREIGN KEY (deployment_id) REFERENCES deployment(deployment_id)
);

-- Index for quick lookup of block traces by block ID.
CREATE INDEX IF NOT EXISTS block_trace_block_id_idx ON block_trace (block_id);

-- Index for quick lookup of block traces by node name.
CREATE INDEX IF NOT EXISTS block_trace_node_name_idx ON block_trace (node_name);

-- Table to store checkpoints within a block trace, including details about the checkpoint and its metadata.
CREATE TABLE IF NOT EXISTS block_trace_checkpoint (
  block_trace_checkpoint_id SERIAL PRIMARY KEY,
  block_trace_id INTEGER NOT NULL,
  source CHAR NOT NULL,
  -- M = main, V = verifier, P = prover
  main_trace BOOLEAN NOT NULL,
  is_control BOOLEAN NOT NULL,
  name VARCHAR NOT NULL,
  started_at FLOAT NOT NULL,
  metadata_json JSONB,
  call_id INT NOT NULL,
  gossip BOOLEAN NOT NULL DEFAULT FALSE,
  FOREIGN KEY (block_trace_id) REFERENCES block_trace(block_trace_id)
);

-- Index for quick lookup of block trace checkpoints by block trace ID and main trace, excluding gossip.
CREATE INDEX IF NOT EXISTS block_trace_checkpoint_block_trace_id_main_trace_gossip 
ON block_trace_checkpoint (block_trace_id, main_trace)
WHERE (NOT gossip);

-- Table to store key-value data associated with a specific node.
CREATE TABLE IF NOT EXISTS data (
  key VARCHAR NOT NULL,
  deployment_id INT NOT NULL,
  node_name VARCHAR NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (key, deployment_id, node_name),
  FOREIGN KEY (deployment_id) REFERENCES deployment(deployment_id)
);

-- Enum type to represent actions in the pool (e.g., received, accepted, rejected).
CREATE TYPE pool_action_t AS ENUM('received', 'accepted', 'rejected');

-- Enum type to represent resource types (e.g., Transaction_diff, Snark_work).
CREATE TYPE resource_t AS ENUM('Transaction_diff', 'Snark_work');

-- Table to store the last block trace gossip checkpoint for a specific resource and action.
CREATE TABLE IF NOT EXISTS last_block_trace_gossip_checkpoint (
  block_trace_id INT NOT NULL,
  resource resource_t NOT NULL,
  action pool_action_t NOT NULL,
  started_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (block_trace_id, resource)
);

-- Table to store transaction traces, including details about the action, time, and metadata.
CREATE TABLE IF NOT EXISTS tx_traces (
  block_trace_checkpoint_id BIGINT NOT NULL,
  node_name VARCHAR(255) NOT NULL,
  action pool_action_t NOT NULL,
  time TIMESTAMPTZ NOT NULL,
  diff JSONB NOT NULL,
  sender VARCHAR(255),
  reason TEXT
);

-- Unique index for transaction traces by block trace checkpoint ID.
CREATE UNIQUE INDEX tx_traces_id ON tx_traces (block_trace_checkpoint_id);

-- Table to store snark work traces, including details about the action, time, and metadata.
CREATE TABLE IF NOT EXISTS sw_traces (
  block_trace_checkpoint_id BIGINT NOT NULL,
  node_name VARCHAR(255) NOT NULL,
  action pool_action_t NOT NULL,
  time TIMESTAMPTZ NOT NULL,
  fst_work_id INT NOT NULL,
  snd_work_id INT,
  fee VARCHAR(255) NOT NULL,
  prover VARCHAR(255) NOT NULL,
  sender VARCHAR(255),
  reason TEXT
);

-- Unique index for snark work traces by block trace checkpoint ID.
CREATE UNIQUE INDEX sw_traces_id ON sw_traces (block_trace_checkpoint_id);

-- Table to store block transactions, including details about the hash, type, and result.
CREATE TABLE IF NOT EXISTS block_txs (
  hash VARCHAR(128) NOT NULL,
  deployment_id INT NOT NULL,
  type VARCHAR(64) NOT NULL,
  memo TEXT,
  block_received TIMESTAMPTZ NOT NULL,
  result VARCHAR(64) NOT NULL,
  PRIMARY KEY (hash)
);

-- Table to store unique transactions, including details about the hash, type, and time.
CREATE TABLE IF NOT EXISTS unique_txs (
  hash VARCHAR(128) NOT NULL,
  deployment_id INT NOT NULL,
  type VARCHAR(64) NOT NULL,
  memo TEXT,
  time TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (hash)
);

CREATE TABLE IF NOT EXISTS EXPERIMENT_STATE (
  name varchar PRIMARY KEY,
  description varchar NOT NULL,
  created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at timestamp DEFAULT CURRENT_TIMESTAMP,
  ended_at timestamp,
  status varchar NOT NULL,
  comment varchar,
  -- PENDING, RUNNING, DONE, ERROR
  -- PENDING: experiment is pending to be started
  -- RUNNING: experiment is running
  -- DONE: experiment is done
  -- ERROR: experiment has an error
  setup_json jsonb NOT NULL,
  current_step_no int,
  current_step_name varchar,
  warnings text[],
  errors text[],
  logs text[]
);