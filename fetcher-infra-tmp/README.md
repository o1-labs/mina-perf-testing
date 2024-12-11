# Infrastructure for the logs fetcher and data persistence components (experimental)

## Pre-requisites

- Some server with the [Docker Compose](https://docs.docker.com/compose/) installed.

## Usage

- Clone this repository.
- Go to cloned repository.
- Copy `.env.example` to `.env` and update the values as needed.
- Run `docker compose up -d --remove-orphans` to start the services.
- Run `docker compose down` to stop the services.

## Useful queries

```sql
-- get latest blocks with breakdown by count of each transaction type
-- mind that 2000 is the limit of lookup in block_trace table, actual number of blocks will be less
-- blocks that have no payments and no zkapp transactions will not be shown

select bt.block_id, bt.time, txs.value ->> 0 type, count(*) cnt
from
  (
    select block_id, max(meta::text)::jsonb meta, to_timestamp(max(time)) time
    from
      (
        select block_id, metadata_json meta, trace_started_at time
        from block_trace
        order by block_trace_id desc
        limit 2000
      ) AS subquery1
    group by block_id
  ) bt
cross join jsonb_array_elements(bt.meta -> 'transactions') txs
where txs.value ->> 0 not in ('coinbase', 'fee_transfer')
group by bt.block_id, bt.time, txs.value ->> 0
order by bt.time desc;
```
