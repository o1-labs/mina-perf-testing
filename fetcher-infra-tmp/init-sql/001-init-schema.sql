CREATE TABLE IF NOT EXISTS block_trace (
  block_trace_id SERIAL PRIMARY KEY,
  node_name varchar NOT NULL,
  block_id varchar NOT NULL,
  trace_started_at float NOT NULL,
  trace_completed_at float,
  total_time float NOT NULL,
  source varchar NOT NULL,
  blockchain_length int NOT NULL,
  global_slot int NOT NULL,
  status varchar NOT NULL,
  metadata_json jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS block_trace_block_id_idx ON block_trace (block_id);

CREATE INDEX IF NOT EXISTS block_trace_node_name_idx ON block_trace (node_name);

CREATE TABLE IF NOT EXISTS block_trace_checkpoint (
  block_trace_checkpoint_id SERIAL PRIMARY KEY,
  block_trace_id integer NOT NULL,
  source char NOT NULL,
  -- M = main, V = verifier, P = prover
  main_trace bool NOT NULL,
  is_control bool NOT NULL,
  name varchar NOT NULL,
  started_at float NOT NULL,
  metadata_json jsonb,
  call_id int NOT NULL,
  gossip bool not null default false,
  FOREIGN KEY (block_trace_id) REFERENCES block_trace(block_trace_id)
);

CREATE INDEX IF NOT EXISTS block_trace_checkpoint_block_trace_id_main_trace_gossip ON block_trace_checkpoint (block_trace_id, main_trace)
WHERE
  (NOT gossip);

CREATE TABLE IF NOT EXISTS data (
  key varchar NOT NULL,
  node_name varchar NOT NULL,
  value text NOT NULL,
  PRIMARY KEY (key, node_name)
);