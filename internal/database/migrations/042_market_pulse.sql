-- Market Pulse is an optional presentation layer over the Ledger. Keep it
-- independently gated so organizers can run the economy without exposing
-- delayed field activity.
INSERT INTO platform_settings (key, value, description, category)
VALUES (
    'market_pulse_enabled',
    'false'::jsonb,
    'Show the privacy-preserving Market Pulse decision surface to participants',
    'general'
)
ON CONFLICT (key) DO NOTHING;
