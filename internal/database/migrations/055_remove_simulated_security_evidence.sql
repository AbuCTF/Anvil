UPDATE flag_share_events
SET submitter_ip = NULL,
    owner_ip = NULL,
    owner_user_agent = NULL,
    submitter_user_agent = NULL,
    request_id = CASE WHEN request_id LIKE 'demo-%' THEN NULL ELSE request_id END,
    country_code = NULL,
    region = NULL,
    city = NULL,
    latitude = NULL,
    longitude = NULL,
    evidence_source = 'redacted'
WHERE evidence_source = 'demo_simulated';
