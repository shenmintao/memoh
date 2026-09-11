-- 0149_push_endpoints
-- Scoped, revocable outbound notification webhooks and content-free delivery receipts.
CREATE TABLE IF NOT EXISTS public.push_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL DEFAULT public.memoh_current_team_id() REFERENCES public.teams(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    bot_id UUID NOT NULL,
    channel_identity_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    token_hash TEXT NOT NULL CHECK (length(token_hash) = 64),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (team_id, id)
);
ALTER TABLE public.push_endpoints DROP CONSTRAINT IF EXISTS push_endpoints_bot_fkey;
ALTER TABLE public.push_endpoints ADD CONSTRAINT push_endpoints_bot_fkey
    FOREIGN KEY (team_id, bot_id) REFERENCES public.bots(team_id, id) ON DELETE CASCADE NOT VALID;
ALTER TABLE public.push_endpoints DROP CONSTRAINT IF EXISTS push_endpoints_identity_fkey;
ALTER TABLE public.push_endpoints ADD CONSTRAINT push_endpoints_identity_fkey
    FOREIGN KEY (team_id, channel_identity_id) REFERENCES public.channel_identities(team_id, id) ON DELETE CASCADE NOT VALID;
CREATE INDEX IF NOT EXISTS idx_push_endpoints_user ON public.push_endpoints(team_id, user_id);

CREATE TABLE IF NOT EXISTS public.push_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL DEFAULT public.memoh_current_team_id() REFERENCES public.teams(id) ON DELETE RESTRICT,
    endpoint_id UUID NOT NULL,
    dedupe_key TEXT NOT NULL CHECK (length(dedupe_key) = 64),
    payload_hash TEXT NOT NULL CHECK (length(payload_hash) = 64),
    status TEXT NOT NULL DEFAULT 'sending' CHECK (status IN ('sending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 1,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (team_id, endpoint_id, dedupe_key)
);
ALTER TABLE public.push_deliveries DROP CONSTRAINT IF EXISTS push_deliveries_endpoint_fkey;
ALTER TABLE public.push_deliveries ADD CONSTRAINT push_deliveries_endpoint_fkey
    FOREIGN KEY (team_id, endpoint_id) REFERENCES public.push_endpoints(team_id, id) ON DELETE CASCADE NOT VALID;
CREATE INDEX IF NOT EXISTS idx_push_deliveries_recent ON public.push_deliveries(team_id, endpoint_id, updated_at DESC);

ALTER TABLE public.push_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.push_endpoints FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS push_endpoints_team ON public.push_endpoints;
CREATE POLICY push_endpoints_team ON public.push_endpoints
    USING (team_id = public.memoh_current_team_id()) WITH CHECK (team_id = public.memoh_current_team_id());
ALTER TABLE public.push_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.push_deliveries FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS push_deliveries_team ON public.push_deliveries;
CREATE POLICY push_deliveries_team ON public.push_deliveries
    USING (team_id = public.memoh_current_team_id()) WITH CHECK (team_id = public.memoh_current_team_id());
