-- Migration: Experiment webhook URL
-- Description: Add experiment_state.webhook_url, the address the orchestrator POSTs to
--              when orchestration ends (in practice n8n's resume URL, which is how
--              "[itn-2] start experiment" learns the run finished).
--              The orchestrator service has written this column since the webhook
--              feature landed, but it only ever existed because GORM's AutoMigrate
--              creates it at process start. A database initialised from these scripts
--              without a subsequent orchestrator restart therefore rejects every
--              experiment with SQLSTATE 42703.
-- Date: 2026-08-13

ALTER TABLE experiment_state ADD COLUMN IF NOT EXISTS webhook_url varchar;
