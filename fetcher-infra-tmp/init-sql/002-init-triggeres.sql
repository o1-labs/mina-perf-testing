alter table
  block_trace_checkpoint alter block_trace_checkpoint_id type bigint;

alter sequence block_trace_checkpoint_block_trace_checkpoint_id_seq as bigint;

CREATE OR REPLACE FUNCTION get_max_deployment_id() RETURNS bigint AS $$
DECLARE
  max_id bigint;
BEGIN
  SELECT MAX(deployment_id) INTO max_id FROM deployment;
  RETURN max_id;
END;
$$ LANGUAGE 'plpgsql';

CREATE
OR REPLACE function gossip_traces_trigger() returns trigger AS $$ DECLARE nresource resource_t;

naction pool_action_t;

nstarted_at timestamptz;

begin NEW.gossip := TRUE;

nstarted_at := to_timestamp(new.started_at);

if new.name in (
  'Snark_work_received',
  'Snark_work_rejected',
  'Snark_work_accepted'
) then nresource := 'Snark_work';

naction := SUBSTRING(
  new.name
  FROM
    12
) :: pool_action_t;

else nresource := 'Transaction_diff';

naction := SUBSTRING(
  new.name
  FROM
    18
) :: pool_action_t;

end if;

insert into
  last_block_trace_gossip_checkpoint (block_trace_id, resource, action, started_at)
values
  (
    new.block_trace_id,
    nresource,
    naction,
    nstarted_at
  ) on conflict on constraint last_block_trace_gossip_checkpoint_pkey do
update
set
  action = naction,
  started_at = nstarted_at;

RETURN NEW;

END;

$$ LANGUAGE 'plpgsql';

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
        'Transaction_diff_received',
        'Transaction_diff_rejected',
        'Transaction_diff_accepted'
    )
) EXECUTE FUNCTION gossip_traces_trigger();

CREATE
OR REPLACE function sw_traces_add(id bigint, block_trace_id_ int4, metadata jsonb) returns setof sw_traces AS $$ begin return query
insert into
  sw_traces (
    block_trace_checkpoint_id,
    node_name,
    action,
    time,
    fst_work_id,
    snd_work_id,
    fee,
    prover,
    sender,
    reason
  ) (
    select
      id,
      node_name,
      action,
      started_at,
      CAST(
        metadata #>> '{work_ids,0}' as integer) fst_work_id,
        CAST(
          metadata #>> '{work_ids,1}' as integer) snd_work_id,
          metadata ->> 'fee' fee,
          metadata ->> 'prover' prover,
          metadata ->> 'sender' sender,
          metadata ->> 'reason' reason
          FROM
            (
              select
                b.node_name,
                p.*
              from
                last_block_trace_gossip_checkpoint p
                join block_trace b on b.block_trace_id = p.block_trace_id
              where
                p.block_trace_id = block_trace_id_
                and p.resource = 'Snark_work'
            ) AS sub
        ) RETURNING *;

END;

$$ LANGUAGE 'plpgsql';

CREATE
OR REPLACE function sw_traces_trigger() returns trigger AS $$ begin
UPDATE
  block_trace_checkpoint bt
SET
  gossip = bt.gossip
  OR (
    select
      COUNT(*) > 0
    from
      sw_traces_add(
        new.block_trace_checkpoint_id,
        new.block_trace_id,
        new.metadata_json
      )
  )
WHERE
  bt.block_trace_checkpoint_id = new.block_trace_checkpoint_id;

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS sw_traces_on_add ON block_trace_checkpoint;
CREATE TRIGGER sw_traces_on_add AFTER INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND new.is_control
    AND new.source = 'M'
    AND new.metadata_json -> 'work_ids' IS NOT NULL
) EXECUTE FUNCTION sw_traces_trigger();

CREATE
OR REPLACE function tx_traces_add(id bigint, block_trace_id_ int4, metadata jsonb) returns setof tx_traces AS $$ begin return query
insert into
  tx_traces (
    block_trace_checkpoint_id,
    node_name,
    action,
    time,
    diff,
    sender,
    reason
  ) (
    select
      id,
      node_name,
      action,
      started_at,
      metadata -> 'diff' diff,
      metadata ->> 'sender' sender,
      metadata ->> 'reason' reason
    FROM
      (
        select
          b.node_name,
          p.*
        from
          last_block_trace_gossip_checkpoint p
          join block_trace b on b.block_trace_id = p.block_trace_id
        where
          p.block_trace_id = block_trace_id_
          and p.resource = 'Transaction_diff'
      ) AS sub
  ) RETURNING *;

END;

$$ LANGUAGE 'plpgsql';

CREATE
OR REPLACE function tx_traces_trigger() returns trigger AS $$ begin
UPDATE
  block_trace_checkpoint bt
SET
  gossip = bt.gossip
  OR (
    select
      COUNT(*) > 0
    from
      tx_traces_add(
        new.block_trace_checkpoint_id,
        new.block_trace_id,
        new.metadata_json
      )
  )
WHERE
  bt.block_trace_checkpoint_id = new.block_trace_checkpoint_id;

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS tx_traces_on_add ON block_trace_checkpoint;
CREATE TRIGGER tx_traces_on_add AFTER INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND new.is_control
    AND new.source = 'M'
    AND new.metadata_json -> 'diff' IS NOT NULL
) EXECUTE FUNCTION tx_traces_trigger();

create
or replace view tx_diffs as
select
  t.deployment_id,
  t.time,
  t.diff,
  t.node_name,
  t.sender,
  t.outcome,
  t.outcome_reason reason,
  t.outcome_time
from
  (
    SELECT
      get_max_deployment_id() deployment_id,
      t1.*,
      LEAD(t1.action) OVER w outcome,
      LEAD(t1.reason) over w outcome_reason,
      LEAD(t1.time) over w outcome_time
    FROM
      tx_traces t1 WINDOW w AS (
        PARTITION BY t1.diff,
        t1.node_name,
        t1.sender
        ORDER BY
          t1.block_trace_checkpoint_id
      )
  ) t
where
  t.action = 'received';

create
or replace view received_txs as
SELECT
  get_max_deployment_id() deployment_id,
  t.node_name,
  t.time,
  t.sender,
  t.outcome_time,
  t.outcome,
  t.reason,
  diff.value ->> 0 "type",
  diff.value ->> 1 hash,
  diff.value ->> 2 memo
FROM
  tx_diffs t
  cross JOIN jsonb_array_elements(t.diff) diff;

CREATE
OR REPLACE function block_txs_trigger() returns trigger AS $$ begin
insert into
  block_txs as bt (deployment_id, hash, type, memo, block_received, result) (
    SELECT
      get_max_deployment_id(),
      txs.value ->> 1,
      txs.value ->> 0,
      txs.value ->> 2,
      to_timestamp(new.trace_started_at),
      txs.value #>> '{3,0}'
    FROM
      jsonb_array_elements(new.metadata_json -> 'transactions') txs
    WHERE
      txs.value ->> 0 NOT IN ('coinbase', 'fee_transfer')
  ) on conflict on constraint block_txs_pkey do
update
set
  block_received = LEAST(bt.block_received, excluded.block_received),
  result = LEAST(bt.result, excluded.result);

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS block_trace_handle_txs ON block_trace;
CREATE TRIGGER block_trace_handle_txs AFTER UPDATE ON block_trace
FOR EACH ROW
WHEN (
    new.metadata_json -> 'transactions' IS NOT NULL
    AND new.trace_started_at > 1500000000
) EXECUTE FUNCTION block_txs_trigger();

create index unique_txs_memo on unique_txs (memo);

CREATE
OR REPLACE function unique_txs_trigger() returns trigger AS $$ begin
insert into
  unique_txs as ut (deployment_id, type, hash, memo, time) (
    select
      get_max_deployment_id(),
      diff.value ->> 0,
      diff.value ->> 1,
      diff.value ->> 2,
      new.time
    from
      jsonb_array_elements(new.diff) diff
  ) on conflict on constraint unique_txs_pkey do
update
set
  time = LEAST(ut.time, excluded.time);

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS tx_traces_handle_txs ON tx_traces;
CREATE TRIGGER tx_traces_handle_txs AFTER INSERT ON tx_traces
FOR EACH ROW
WHEN (new.action = 'received') EXECUTE FUNCTION unique_txs_trigger();

create
or replace view txs as
select
  u.deployment_id,
  coalesce(b.hash, u.hash) hash,
  coalesce(b.type, u.type) "type",
  coalesce(b.memo, u.memo) memo,
  u.time,
  b.block_received,
  b.block_received - u.time latency,
  b.result
from
  unique_txs u full
  join block_txs b on u.hash = b.hash and u.deployment_id = b.deployment_id;

create
or replace view experiments as with txs_ as (
  -- we're aiming to process transactions with memos of form {name}-{round}-{batch}-{ix}
  --                                                                  -3       -2    -1
  select
    regexp_replace(memo, '-\d+-\d+-\d+$', '') as exp,
    cast((regexp_matches(memo, '^(.*)-(\d+)-(\d+)-(\d+)$'))[2] as int) as round,
    txs.deployment_id,
    txs.hash,
    txs.type,
    txs.memo,
    txs.time,
    txs.block_received,
    txs.latency,
    txs.result
  from
    txs
  where
    memo ~ '-\d+-\d+-\d+$'
)
select
  t.exp,
  t.round,
  t.deployment_id,
  round(t.total * 60.0 / t.duration_sec, 2) as rate_min,
  round(t.zkapps * 60.0 / t.duration_sec, 2) as zkapp_rate_min,
  round((t.total - t.zkapps) * 60.0 / t.duration_sec, 2) as payment_rate_min,
  date_trunc('seconds', t.last_tx_time - t.first_tx_time) as duration,
  t.max_latency,
  round(
    (
      select
        count(*)
      from
        txs_
      where
        latency is null
        and exp = t.exp
        and round = t.round
    ) * 1.0 / t.total,
    2
  ) as missed,
  round(
    (
      select
        count(*)
      from
        txs_
      where
        result = 'Failed'
        and exp = t.exp
        and round = t.round
    ) * 1.0 / t.total,
    2
  ) as failed,
  round(
    (
      select
        count(*)
      from
        txs_
      where
        result = 'Applied'
        and exp = t.exp
        and round = t.round
    ) * 1.0 / t.total,
    2
  ) as successful,
  t.first_tx_time as start,
  t.total,
  t.zkapps
from
  (
    select
      exp,
      round,
      deployment_id,
      count(*) as total,
      count(nullif(type, 'payment')) as zkapps,
      min(time) as first_tx_time,
      max(time) as last_tx_time,
      max(latency) as max_latency,
      nullif(
        extract(
          epoch
          from
            max(time) - min(time)
        ) :: int,
        0
      ) as duration_sec
    from
      txs_
    group by
      exp,
      round,
      deployment_id
  ) t
where
  t.round is not null and
  t.deployment_id is not null

order by
  first_tx_time;

CREATE
OR REPLACE function block_trace_update_started_at_trigger() returns trigger AS $$ begin
update
  block_trace
set
  trace_started_at = LEAST(nullif(trace_started_at, -1), new.started_at),
  trace_completed_at = GREATEST(nullif(trace_completed_at, -1), new.started_at),
  total_time = GREATEST(nullif(trace_completed_at, -1), new.started_at) - LEAST(nullif(trace_started_at, -1), new.started_at)
where
  block_trace_id = new.block_trace_id
  and status <> 'Success';

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS block_trace_update_started_at ON block_trace_checkpoint;
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
        'Transaction_diff_received',
        'Transaction_diff_rejected',
        'Transaction_diff_accepted'
    )
) EXECUTE FUNCTION block_trace_update_started_at_trigger();

CREATE
OR REPLACE function block_trace_update_status_trigger() returns trigger AS $$ begin if new.name = 'Breadcrumb_integrated' then
update
  block_trace
set
  status = 'Success'
where
  block_trace_id = new.block_trace_id;

else
update
  block_trace
set
  status = 'Failure'
where
  block_trace_id = new.block_trace_id
  and status <> 'Success';

end if;

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS block_trace_update_status ON block_trace_checkpoint;
CREATE TRIGGER block_trace_update_status AFTER INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND NOT new.is_control
    AND new.source = 'M'
    AND new.name IN ('Breadcrumb_integrated', 'Failure')
) EXECUTE FUNCTION block_trace_update_status_trigger();

CREATE
OR REPLACE function block_trace_update_source_trigger() returns trigger AS $$ declare new_source varchar(16);

begin if new.name = 'External_block_received' then new_source = 'External';

elsif new.name = 'Begin_block_production' then new_source = 'Internal';

elsif new.name = 'To_download' then new_source = 'Catchup';

else new_source = 'Reconstruct';

end if;

update
  block_trace
set
  source = new_source
where
  block_trace_id = new.block_trace_id
  and source = 'Unknown';

RETURN NULL;

END;

$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS block_trace_update_source ON block_trace_checkpoint;
CREATE TRIGGER block_trace_update_source AFTER INSERT ON block_trace_checkpoint
FOR EACH ROW
WHEN (
    new.main_trace
    AND NOT new.is_control
    AND new.source = 'M'
    AND new.name IN (
        'External_block_received',
        'Begin_block_production',
        'To_download',
        'Loaded_transition_from_storage'
    )
) EXECUTE FUNCTION block_trace_update_source_trigger();