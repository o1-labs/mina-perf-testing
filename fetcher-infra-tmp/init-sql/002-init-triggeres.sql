alter table
  block_trace_checkpoint alter block_trace_checkpoint_id type bigint;

alter sequence block_trace_checkpoint_block_trace_checkpoint_id_seq as bigint;

create type pool_action_t as ENUM('received', 'accepted', 'rejected');

create type resource_t as ENUM('Transaction_diff', 'Snark_work');

create table if not exists last_block_trace_gossip_checkpoint (
  block_trace_id int4 not null,
  resource resource_t not null,
  action pool_action_t not null,
  started_at timestamptz not null,
  primary key (block_trace_id, resource)
);

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

create
or replace trigger gossip_traces_on_add before
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and not new.is_control
    and new.source = 'M'
    and new.name in (
      'Snark_work_received',
      'Snark_work_rejected',
      'Snark_work_accepted',
      'Transaction_diff_received',
      'Transaction_diff_rejected',
      'Transaction_diff_accepted'
    )
  ) execute function gossip_traces_trigger();

create table tx_traces (
  block_trace_checkpoint_id bigint not null,
  node_name varchar(255) not null,
  action pool_action_t not null,
  time timestamptz not null,
  diff jsonb not null,
  sender varchar(255),
  reason text
);

CREATE UNIQUE INDEX tx_traces_id ON tx_traces (block_trace_checkpoint_id);

create table sw_traces (
  block_trace_checkpoint_id bigint not NULL,
  node_name varchar(255) not NULL,
  action pool_action_t not NULL,
  time timestamptz not NULL,
  fst_work_id int4 not NULL,
  snd_work_id int4,
  fee varchar(255) not NULL,
  prover varchar(255) not null,
  sender varchar(255),
  reason text
);

CREATE UNIQUE INDEX sw_traces_id ON sw_traces (block_trace_checkpoint_id);

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

create
or replace trigger sw_traces_on_add
after
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and new.is_control
    and new.source = 'M'
    and new.metadata_json -> 'work_ids' is not NULL
  ) execute function sw_traces_trigger();

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

create
or replace trigger tx_traces_on_add
after
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and new.is_control
    and new.source = 'M'
    and new.metadata_json -> 'diff' is not NULL
  ) execute function tx_traces_trigger();

create
or replace view tx_diffs as
select
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
  t.node_name,
  t.time,
  t.sender,
  t.outcome_time,
  t.outcome,
  t.reason,
  diff.value ->> 0 type,
  diff.value ->> 1 hash,
  diff.value ->> 2 memo
FROM
  tx_diffs t
  cross JOIN jsonb_array_elements(t.diff) diff;

create table block_txs (
  hash varchar(128) not null,
  type varchar(64) not null,
  memo text,
  block_received timestamptz not null,
  result varchar(64) not null,
  primary key (hash)
);

CREATE
OR REPLACE function block_txs_trigger() returns trigger AS $$ begin
insert into
  block_txs as bt (hash, type, memo, block_received, result) (
    SELECT
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

create
or replace trigger block_trace_handle_txs
after
update
  on block_trace for each row
  when (
    new.metadata_json -> 'transactions' is not null
    and new.trace_started_at > 1500000000
  ) execute function block_txs_trigger();

create table unique_txs (
  hash varchar(128) not null,
  type varchar(64) not null,
  memo text,
  time timestamptz not null,
  primary key (hash)
);

create index unique_txs_memo on unique_txs (memo);

CREATE
OR REPLACE function unique_txs_trigger() returns trigger AS $$ begin
insert into
  unique_txs as ut (type, hash, memo, time) (
    select
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

create
or replace trigger tx_traces_handle_txs
after
insert
  on tx_traces for each row
  when (new.action = 'received') execute function unique_txs_trigger();

create
or replace view txs as
select
  coalesce(b.hash, u.hash) hash,
  coalesce(b.type, u.type) type,
  coalesce(b.memo, u.memo) memo,
  u.time,
  b.block_received,
  b.block_received - u.time latency,
  b.result
from
  unique_txs u full
  join block_txs b on u.hash = b.hash;

create
or replace view experiments as with txs_ as (
  select
    split_part(memo, '-', -2) as exp,
    cast(
      nullif(
        regexp_replace(split_part(memo, '-', -1), '[^\d]', ''),
        ''
      ) as int
    ) as round,
    *
  from
    txs
  where
    memo like '%-%' -- Only include rows where memo contains a dash
)
select
  t.exp,
  t.round,
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
      round
  ) t
where
  t.round is not null
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

create
or replace trigger block_trace_update_started_at
after
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and not new.is_control
    and new.source = 'M'
    and new.name not in (
      'Snark_work_received',
      'Snark_work_rejected',
      'Snark_work_accepted',
      'Transaction_diff_received',
      'Transaction_diff_rejected',
      'Transaction_diff_accepted'
    )
  ) execute function block_trace_update_started_at_trigger();

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

create
or replace trigger block_trace_update_status
after
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and not new.is_control
    and new.source = 'M'
    and new.name in ('Breadcrumb_integrated', 'Failure')
  ) execute function block_trace_update_status_trigger();

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

create
or replace trigger block_trace_update_source
after
insert
  on block_trace_checkpoint for each row
  when (
    new.main_trace
    and not new.is_control
    and new.source = 'M'
    and new.name in (
      'External_block_received',
      'Begin_block_production',
      'To_download',
      'Loaded_transition_from_storage'
    )
  ) execute function block_trace_update_source_trigger();