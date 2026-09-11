-- 0149_push_endpoints
-- Remove notification webhook receipts before their owning endpoints.
DROP TABLE IF EXISTS public.push_deliveries;
DROP TABLE IF EXISTS public.push_endpoints;
