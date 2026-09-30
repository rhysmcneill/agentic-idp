-- The worker registers this itself at startup from its own local config —
-- the control plane never decides it, only stores what the worker reports.
ALTER TABLE worker_credentials
    ADD COLUMN ci_callback_url text NULL;
